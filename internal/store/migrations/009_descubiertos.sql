-- Lo descubierto (unidades y timers de systemd, LXC y VMs de Proxmox) va a
-- la bandeja de Descubiertos y no a la portada. "Mostrar" lo pasa a la
-- portada: la clave es "found:<host>/<tipo>/<nombre>".

CREATE TABLE shown (
    key TEXT    PRIMARY KEY,
    at  INTEGER NOT NULL -- ms Unix
) STRICT, WITHOUT ROWID;
