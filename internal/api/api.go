package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"github.com/jonataazevedo/go-react/internal/store/pgstore"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"
)

type apiHandler struct {
	q           *pgstore.Queries
	r           *chi.Mux
	upgrader    websocket.Upgrader
	subscribers map[string]map[*websocket.Conn]context.CancelFunc
	mu          *sync.Mutex
}

func (h apiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.r.ServeHTTP(w, r)
}

func NewHandler(q *pgstore.Queries) http.Handler {
	a := apiHandler{
		q:           q,
		upgrader:    websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
		subscribers: make(map[string]map[*websocket.Conn]context.CancelFunc),
		mu:          &sync.Mutex{},
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, middleware.Logger)

	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	r.Get("/subscribe/{sala_id}", a.handleSubscribe)

	r.Route("/api", func(r chi.Router) {
		r.Route("/salas", func(r chi.Router) {
			r.Post("/", a.handleCreateSala)
			r.Get("/", a.handleGetSalas)

			r.Route("/{sala_id}", func(r chi.Router) {
				r.Get("/", a.handleGetSala)

				r.Route("/messages", func(r chi.Router) {
					r.Post("/", a.handleCreateSalaMessage)
					r.Get("/", a.handleGetSalaMessages)

					r.Route("/{message_id}", func(r chi.Router) {
						r.Get("/", a.handleGetSalaMessage)
						r.Patch("/react", a.handleReactToMessage)
						r.Delete("/react", a.handleRemoveReactFromMessage)
						r.Patch("/answer", a.handleMarkMessageAsAnswered)
					})
				})
			})
		})
	})

	a.r = r
	return a
}

const (
	MessageKindMessageCreated          = "message_created"
	MessageKindMessageRactionIncreased = "message_reaction_increased"
	MessageKindMessageRactionDecreased = "message_reaction_decreased"
	MessageKindMessageAnswered         = "message_answered"
)

type MessageMessageReactionIncreased struct {
	ID    string `json:"id"`
	Count int64  `json:"count"`
}

type MessageMessageReactionDecreased struct {
	ID    string `json:"id"`
	Count int64  `json:"count"`
}

type MessageMessageAnswered struct {
	ID string `json:"id"`
}

type MessageMessageCreated struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

type Message struct {
	Kind   string `json:"kind"`
	Value  any    `json:"value"`
	SalaID string `json:"-"`
}

func (h apiHandler) notifyClients(msg Message) {
	h.mu.Lock()
	defer h.mu.Unlock()

	subscribers, ok := h.subscribers[msg.SalaID]
	if !ok || len(subscribers) == 0 {
		return
	}

	for conn, cancel := range subscribers {
		if err := conn.WriteJSON(msg); err != nil {
			slog.Error("failed to send message to client", "error", err)
			cancel()
		}
	}
}

func (h apiHandler) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	_, rawSalaID, _, ok := h.readSala(w, r)
	if !ok {
		return
	}

	c, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Warn("failed to upgrade connection", "error", err)
		http.Error(w, "failed to upgrade to ws connection", http.StatusBadRequest)
		return
	}

	defer c.Close()

	ctx, cancel := context.WithCancel(r.Context())

	h.mu.Lock()
	if _, ok := h.subscribers[rawSalaID]; !ok {
		h.subscribers[rawSalaID] = make(map[*websocket.Conn]context.CancelFunc)
	}
	slog.Info("new client connected", "sala_id", rawSalaID, "client_ip", r.RemoteAddr)
	h.subscribers[rawSalaID][c] = cancel
	h.mu.Unlock()

	<-ctx.Done()

	h.mu.Lock()
	delete(h.subscribers[rawSalaID], c)
	h.mu.Unlock()
}

func (h apiHandler) handleCreateSala(w http.ResponseWriter, r *http.Request) {
	type _body struct {
		Theme string `json:"theme"`
	}
	var body _body
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	salaID, err := h.q.InsertSala(r.Context(), body.Theme)
	if err != nil {
		slog.Error("failed to insert sala", "error", err)
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return
	}

	type response struct {
		ID string `json:"id"`
	}

	sendJSON(w, response{ID: salaID.String()})
}

func (h apiHandler) handleGetSalas(w http.ResponseWriter, r *http.Request) {
	salas, err := h.q.GetSalas(r.Context())
	if err != nil {
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		slog.Error("failed to get salas", "error", err)
		return
	}

	if salas == nil {
		salas = []pgstore.Sala{}
	}

	sendJSON(w, salas)
}

func (h apiHandler) handleGetSala(w http.ResponseWriter, r *http.Request) {
	sala, _, _, ok := h.readSala(w, r)
	if !ok {
		return
	}

	sendJSON(w, sala)
}

func (h apiHandler) handleCreateSalaMessage(w http.ResponseWriter, r *http.Request) {
	_, rawSalaID, salaID, ok := h.readSala(w, r)
	if !ok {
		return
	}

	type _body struct {
		Message string `json:"message"`
	}
	var body _body
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	messageID, err := h.q.InsertMessage(r.Context(), pgstore.InsertMessageParams{SalaID: salaID, Message: body.Message})
	if err != nil {
		slog.Error("failed to insert message", "error", err)
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return
	}

	type response struct {
		ID string `json:"id"`
	}

	sendJSON(w, response{ID: messageID.String()})

	go h.notifyClients(Message{
		Kind:   MessageKindMessageCreated,
		SalaID: rawSalaID,
		Value: MessageMessageCreated{
			ID:      messageID.String(),
			Message: body.Message,
		},
	})
}

func (h apiHandler) handleGetSalaMessages(w http.ResponseWriter, r *http.Request) {
	_, _, salaID, ok := h.readSala(w, r)
	if !ok {
		return
	}

	messages, err := h.q.GetSalaMessages(r.Context(), salaID)
	if err != nil {
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		slog.Error("failed to get sala messages", "error", err)
		return
	}

	if messages == nil {
		messages = []pgstore.Message{}
	}

	sendJSON(w, messages)
}

func (h apiHandler) handleGetSalaMessage(w http.ResponseWriter, r *http.Request) {
	_, _, _, ok := h.readSala(w, r)
	if !ok {
		return
	}

	rawMessageID := chi.URLParam(r, "message_id")
	messageID, err := uuid.Parse(rawMessageID)
	if err != nil {
		http.Error(w, "invalid message id", http.StatusBadRequest)
		return
	}

	messages, err := h.q.GetMessage(r.Context(), messageID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "message not found", http.StatusBadRequest)
			return
		}

		slog.Error("failed to get message", "error", err)
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return
	}

	sendJSON(w, messages)
}

func (h apiHandler) handleReactToMessage(w http.ResponseWriter, r *http.Request) {
	_, rawSalaID, _, ok := h.readSala(w, r)
	if !ok {
		return
	}

	rawID := chi.URLParam(r, "message_id")
	id, err := uuid.Parse(rawID)
	if err != nil {
		http.Error(w, "invalid message id", http.StatusBadRequest)
		return
	}

	count, err := h.q.ReactToMessage(r.Context(), id)
	if err != nil {
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		slog.Error("failed to react to message", "error", err)
		return
	}

	type response struct {
		Count int64 `json:"count"`
	}

	sendJSON(w, response{Count: count})

	go h.notifyClients(Message{
		Kind:   MessageKindMessageRactionIncreased,
		SalaID: rawSalaID,
		Value: MessageMessageReactionIncreased{
			ID:    rawID,
			Count: count,
		},
	})
}

func (h apiHandler) handleRemoveReactFromMessage(w http.ResponseWriter, r *http.Request) {
	_, rawSalaID, _, ok := h.readSala(w, r)
	if !ok {
		return
	}

	rawID := chi.URLParam(r, "message_id")
	id, err := uuid.Parse(rawID)
	if err != nil {
		http.Error(w, "invalid message id", http.StatusBadRequest)
		return
	}

	count, err := h.q.RemoveReactionFromMessage(r.Context(), id)
	if err != nil {
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		slog.Error("failed to react to message", "error", err)
		return
	}

	type response struct {
		Count int64 `json:"count"`
	}

	sendJSON(w, response{Count: count})

	go h.notifyClients(Message{
		Kind:   MessageKindMessageRactionDecreased,
		SalaID: rawSalaID,
		Value: MessageMessageReactionDecreased{
			ID:    rawID,
			Count: count,
		},
	})
}

func (h apiHandler) handleMarkMessageAsAnswered(w http.ResponseWriter, r *http.Request) {
	_, rawSalaID, _, ok := h.readSala(w, r)
	if !ok {
		return
	}

	rawID := chi.URLParam(r, "message_id")
	id, err := uuid.Parse(rawID)
	if err != nil {
		http.Error(w, "invalid message id", http.StatusBadRequest)
		return
	}

	err = h.q.MarkMessageAsAnswered(r.Context(), id)
	if err != nil {
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		slog.Error("failed to react to message", "error", err)
		return
	}

	w.WriteHeader(http.StatusOK)

	go h.notifyClients(Message{
		Kind:   MessageKindMessageAnswered,
		SalaID: rawSalaID,
		Value: MessageMessageAnswered{
			ID: rawID,
		},
	})
}
