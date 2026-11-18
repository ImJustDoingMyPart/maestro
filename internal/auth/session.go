package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ImJustDoingMyPart/maestro/internal/store"
)

// CookieName es el nombre de la cookie de sesión.
const CookieName = "maestro_session"

// SessionTTL es cuánto dura una sesión.
const SessionTTL = 30 * 24 * time.Hour

// ErrBadCredentials sale de Login si el usuario no existe o la contraseña no
// coincide. No distingue los dos casos a propósito.
var ErrBadCredentials = errors.New("usuario o contraseña incorrectos")

// dummyHash se verifica cuando el usuario no existe, para que esa respuesta
// tarde lo mismo que la de una contraseña incorrecta y el tiempo no diga qué
// usuarios hay.
var dummyHash = func() string {
	h, err := HashPassword("contraseña-que-nadie-usa")
	if err != nil {
		panic(err)
	}
	return h
}()

// Sessions maneja login, logout y la cookie.
type Sessions struct {
	store  *store.Store
	secure bool // cookie Secure: false solo en --dev, sin HTTPS
	logger *slog.Logger
	now    func() time.Time
}

// NewSessions crea el manejador. secure debe ser true salvo en desarrollo
// local por HTTP.
func NewSessions(st *store.Store, secure bool, logger *slog.Logger) *Sessions {
	return &Sessions{store: st, secure: secure, logger: logger, now: time.Now}
}

// Login verifica las credenciales y, si están bien, crea la sesión y pone la
// cookie en w.
func (s *Sessions) Login(ctx context.Context, w http.ResponseWriter, username, password string) error {
	u, err := s.store.UserByName(ctx, username)
	hash := u.PasswordHash
	if errors.Is(err, store.ErrNotFound) {
		hash = dummyHash
	} else if err != nil {
		return err
	}

	ok, err := VerifyPassword(password, hash)
	if err != nil {
		return err
	}
	if !ok || u.ID == 0 {
		return ErrBadCredentials
	}

	token, tokenHash, err := newToken()
	if err != nil {
		return err
	}
	now := s.now()
	if err := s.store.CreateSession(ctx, tokenHash, u.ID, now, now.Add(SessionTTL)); err != nil {
		return err
	}
	// Barrer las vencidas al entrar alcanza: los logins son pocos.
	if _, err := s.store.DeleteExpiredSessions(ctx, now); err != nil {
		s.logger.Warn("borrar sesiones vencidas", "err", err)
	}

	http.SetCookie(w, s.cookie(token, int(SessionTTL.Seconds())))
	return nil
}

// MinPasswordLen es el largo mínimo de una contraseña.
const MinPasswordLen = 12

// Errores de Setup.
var (
	ErrSetupDone    = errors.New("ya hay un usuario: entrá con él")
	ErrWeakPassword = fmt.Errorf("la contraseña tiene que tener al menos %d caracteres", MinPasswordLen)
	ErrBadUsername  = errors.New("el usuario no puede estar vacío, tener espacios ni pasar de 64 caracteres")
)

// Setup crea el primer usuario y entra con él. Solo funciona mientras no
// haya ningún usuario: después devuelve ErrSetupDone.
func (s *Sessions) Setup(ctx context.Context, w http.ResponseWriter, username, password string) error {
	if username == "" || len(username) > 64 || strings.ContainsFunc(username, unicode.IsSpace) {
		return ErrBadUsername
	}
	if utf8.RuneCountInString(password) < MinPasswordLen {
		return ErrWeakPassword
	}
	// Barato antes del hash (argon2id tarda): si ya hay usuario, ni se
	// calcula. CreateFirstUser lo vuelve a mirar de forma atómica.
	if has, err := s.store.HasUsers(ctx); err != nil {
		return err
	} else if has {
		return ErrSetupDone
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if err := s.store.CreateFirstUser(ctx, username, hash, s.now()); errors.Is(err, store.ErrUsersExist) {
		return ErrSetupDone
	} else if err != nil {
		return err
	}
	return s.Login(ctx, w, username, password)
}

// NeedsSetup dice si todavía no hay ningún usuario.
func (s *Sessions) NeedsSetup(ctx context.Context) (bool, error) {
	has, err := s.store.HasUsers(ctx)
	return !has, err
}

// Logout borra la sesión de la petición y la cookie.
func (s *Sessions) Logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(CookieName); err == nil {
		if err := s.store.DeleteSession(r.Context(), hashToken(c.Value)); err != nil {
			return err
		}
	}
	http.SetCookie(w, s.cookie("", -1))
	return nil
}

type userKey struct{}

// UserFrom devuelve el usuario que RequireLogin puso en el contexto.
func UserFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(userKey{}).(store.User)
	return u, ok
}

// RequireLogin deja pasar solo peticiones con una sesión vigente. Al resto
// las manda a /login con un redirect (la aplicación, que pide con fetch, ve
// que terminó en /login y va ahí).
func (s *Sessions) RequireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.userFromRequest(r)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.logger.Error("leer sesión", "err", err)
				http.Error(w, "error interno", http.StatusInternalServerError)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
	})
}

// LoggedIn dice si la petición trae una sesión vigente.
func (s *Sessions) LoggedIn(r *http.Request) bool {
	_, err := s.userFromRequest(r)
	return err == nil
}

func (s *Sessions) userFromRequest(r *http.Request) (store.User, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return store.User{}, store.ErrNotFound
	}
	return s.store.SessionUser(r.Context(), hashToken(c.Value), s.now())
}

func (s *Sessions) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true, // JavaScript no la puede leer
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// newToken genera 32 bytes aleatorios: el token va en la cookie y su hash
// en la base.
func newToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
