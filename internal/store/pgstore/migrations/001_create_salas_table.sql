CREATE TABLE IF NOT EXISTS salas (
    "id"                uuid            PRIMARY KEY     NOT NULL    DEFAULT gen_random_uuid(),
    "theme"             VARCHAR(225)                    NOT NULL
);

---- create above / drop below ----

DROP TABLE IF EXISTS salas;