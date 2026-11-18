-- Avisos a Telegram que todavía no salieron: el que avisa los escribe acá
-- antes de mandarlos y los borra cuando salen (o cuando Telegram los
-- rechaza para siempre). Así un aviso que espera reintento sobrevive a un
-- reinicio del hub. chat es la posición en la lista de chats de
-- maestro.yaml, no el chat: ese es parte de la configuración secreta.

CREATE TABLE alert_outbox (
    id      INTEGER PRIMARY KEY,
    created INTEGER NOT NULL, -- ms Unix
    chat    INTEGER NOT NULL,
    service TEXT    NOT NULL,
    text    TEXT    NOT NULL
) STRICT;
