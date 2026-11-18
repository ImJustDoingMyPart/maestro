package hub

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// failLimiter cuenta intentos fallidos por clave (la IP del cliente) en
// una ventana corrida. Con max fallas dentro de window, la clave queda
// frenada hasta que la más vieja salga de la ventana. Un intento bueno
// borra la cuenta de su clave.
//
// Es para el login: el costo de argon2id ya frena cada intento, y esto
// pone un techo a cuántos puede probar una misma IP. Vive en memoria: al
// reiniciar el hub la cuenta empieza de cero, que para un panel de una
// sola casa alcanza.
type failLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	fails  map[string][]time.Time
}

// maxLimiterKeys es el tope de claves en memoria. Pasado, se tiran las
// que no tienen fallas dentro de la ventana; si siguen siendo demasiadas,
// todas (un ataque desde miles de IPs no tiene que llenar la memoria).
const maxLimiterKeys = 10000

func newFailLimiter(max int, window time.Duration) *failLimiter {
	return &failLimiter{max: max, window: window, now: time.Now, fails: map[string][]time.Time{}}
}

// Blocked dice si key está frenada y, si lo está, cuánto falta.
func (l *failLimiter) Blocked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	f := l.recent(key, now)
	if len(f) < l.max {
		return false, 0
	}
	return true, f[0].Add(l.window).Sub(now)
}

// Fail anota un intento fallido de key.
func (l *failLimiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.fails) >= maxLimiterKeys {
		for k := range l.fails {
			l.recent(k, now)
		}
		if len(l.fails) >= maxLimiterKeys {
			clear(l.fails)
		}
	}
	l.fails[key] = append(l.recent(key, now), now)
}

// Reset borra la cuenta de key (después de un intento bueno).
func (l *failLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

// recent deja en el mapa solo las fallas de key dentro de la ventana y las
// devuelve. Con l.mu tomado.
func (l *failLimiter) recent(key string, now time.Time) []time.Time {
	f := l.fails[key]
	i := 0
	for i < len(f) && now.Sub(f[i]) >= l.window {
		i++
	}
	f = f[i:]
	if len(f) == 0 {
		delete(l.fails, key)
		return nil
	}
	l.fails[key] = f
	return f
}

// clientIP es la IP de quien hace el pedido. Detrás de Caddy, RemoteAddr
// es Caddy y la del cliente es la última de X-Forwarded-For: Caddy la
// agrega al final, así que lo que mande el cliente en ese header queda
// antes y no sirve para cambiar de clave. Sin el header (directo, o en
// --dev), RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
