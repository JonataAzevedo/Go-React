package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/jonataazevedo/go-react/internal/store/pgstore"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h apiHandler) readSala(
	w http.ResponseWriter,
	r *http.Request,
) (sala pgstore.Sala, rawSalaID string, salaID uuid.UUID, ok bool) {
	rawSalaID = chi.URLParam(r, "sala_id")
	salaID, err := uuid.Parse(rawSalaID)
	if err != nil {
		http.Error(w, "invalid sala id", http.StatusBadRequest)
		return pgstore.Sala{}, "", uuid.UUID{}, false
	}

	sala, err = h.q.GetSala(r.Context(), salaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.Error(w, "sala not found", http.StatusBadRequest)
			return pgstore.Sala{}, "", uuid.UUID{}, false
		}

		slog.Error("failed to get sala", "error", err)
		http.Error(w, "something went wrong", http.StatusInternalServerError)
		return pgstore.Sala{}, "", uuid.UUID{}, false
	}

	return sala, rawSalaID, salaID, true
}

func sendJSON(w http.ResponseWriter, rawData any) {
	data, _ := json.Marshal(rawData)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
