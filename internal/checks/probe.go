package checks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

// maxBody es cuánto del cuerpo de la respuesta se lee (y se descarta): lo
// justo para que el servidor termine de mandar sin que una página enorme
// ocupe la red del hub.
const maxBody = 1 << 20

// maxRedirects es cuántas redirecciones se siguen como mucho.
const maxRedirects = 10

// outcome es lo que da un probe, sin la hora ni a qué check pertenece.
type outcome struct {
	ok         bool
	latency    time.Duration
	detail     string
	certExpiry time.Time
}

// httpProber hace el check HTTP de un Spec. Guarda su propio cliente: cada
// check tiene su timeout y su política de redirecciones.
type httpProber struct {
	spec   Spec
	client *http.Client
}

func newHTTPProber(spec Spec) *httpProber {
	tr := &http.Transport{
		// Sin proxy del entorno: el check mide si el hub llega al servicio,
		// no si llega un proxy.
		Proxy: nil,
		// Una conexión nueva cada vez: reusar una abierta escondería que el
		// servicio dejó de aceptar conexiones.
		DisableKeepAlives:   true,
		TLSHandshakeTimeout: spec.timeout(),
		ForceAttemptHTTP2:   true,
	}
	c := &http.Client{
		Transport: tr,
		Timeout:   spec.timeout(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if spec.NoRedirects {
				return http.ErrUseLastResponse // el 3xx es la respuesta
			}
			if len(via) >= maxRedirects {
				return fmt.Errorf("más de %d redirecciones", maxRedirects)
			}
			return nil
		},
	}
	return &httpProber{spec: spec, client: c}
}

func (p *httpProber) probe(ctx context.Context) outcome {
	ctx, cancel := context.WithTimeout(ctx, p.spec.timeout())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.spec.Target, nil)
	if err != nil {
		return outcome{detail: err.Error()}
	}
	req.Header.Set("User-Agent", "Maestro-check/1")

	start := time.Now()
	resp, err := p.client.Do(req)
	latency := time.Since(start)
	if err != nil {
		return outcome{latency: latency, detail: describeErr(err, p.spec)}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))

	o := outcome{latency: latency}
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		o.certExpiry = resp.TLS.PeerCertificates[0].NotAfter
	}
	if p.spec.Expect != 0 {
		o.ok = resp.StatusCode == p.spec.Expect
		if !o.ok {
			o.detail = fmt.Sprintf("código %d (esperado %d)", resp.StatusCode, p.spec.Expect)
		}
	} else {
		o.ok = resp.StatusCode >= 200 && resp.StatusCode <= 299
		if !o.ok {
			o.detail = fmt.Sprintf("código %d (esperado 2xx)", resp.StatusCode)
		}
	}
	if o.ok {
		o.detail = fmt.Sprintf("código %d", resp.StatusCode)
	}
	return o
}

// probeTCP conecta y cierra.
func probeTCP(ctx context.Context, spec Spec) outcome {
	ctx, cancel := context.WithTimeout(ctx, spec.timeout())
	defer cancel()
	var d net.Dialer
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", spec.Target)
	latency := time.Since(start)
	if err != nil {
		return outcome{latency: latency, detail: describeErr(err, spec)}
	}
	_ = conn.Close()
	return outcome{ok: true, latency: latency, detail: "conectó"}
}

// probeDNS pregunta el nombre al servidor del check (o al del hub) y pasa
// si da al menos una dirección. El detalle lleva las primeras.
func probeDNS(ctx context.Context, spec Spec) outcome {
	ctx, cancel := context.WithTimeout(ctx, spec.timeout())
	defer cancel()
	r := net.DefaultResolver
	if spec.Resolver != "" {
		r = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, spec.Resolver)
		}}
	}
	start := time.Now()
	addrs, err := r.LookupHost(ctx, spec.Target)
	latency := time.Since(start)
	if err != nil {
		return outcome{latency: latency, detail: describeDNSErr(err, spec)}
	}
	if len(addrs) == 0 {
		return outcome{latency: latency, detail: "respondió sin direcciones"}
	}
	shown := addrs[:min(len(addrs), 3)]
	detail := "responde " + strings.Join(shown, ", ")
	if len(addrs) > len(shown) {
		detail += fmt.Sprintf(" y %d más", len(addrs)-len(shown))
	}
	return outcome{ok: true, latency: latency, detail: detail}
}

// describeDNSErr es describeErr para un check DNS: el nombre que se pregunta
// es lo que se vigila, así que "no existe" es la respuesta del servidor y
// no un problema para llegar a él.
func describeDNSErr(err error, spec Spec) string {
	var dnsErr *net.DNSError
	switch {
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return "el servidor dice que " + spec.Target + " no existe"
	case errors.As(err, &dnsErr) && dnsErr.IsTimeout, errors.Is(err, context.DeadlineExceeded):
		return fmt.Sprintf("timeout (%s)", formatDuration(spec.timeout()))
	case errors.Is(err, syscall.ECONNREFUSED):
		return "conexión rechazada"
	case errors.As(err, &dnsErr):
		return "falla: " + dnsErr.Err
	}
	return err.Error()
}

// describeErr pasa un error de red a un texto corto en castellano. Es lo
// que se ve en la página del servicio y en los eventos, así que conviene
// que sea estable: el mismo problema, el mismo texto.
func describeErr(err error, spec Spec) string {
	var (
		dnsErr   *net.DNSError
		certErr  x509.CertificateInvalidError
		authErr  x509.UnknownAuthorityError
		hostErr  x509.HostnameError
		verifErr *tls.CertificateVerificationError
		netErr   net.Error
	)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()):
		return fmt.Sprintf("timeout (%s)", spec.timeout())
	case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		return "no resuelve " + dnsErr.Name
	case errors.As(err, &dnsErr):
		return "error de DNS: " + dnsErr.Err
	case errors.Is(err, syscall.ECONNREFUSED):
		return "conexión rechazada"
	case errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH):
		return "host inalcanzable"
	case errors.Is(err, syscall.ECONNRESET):
		return "conexión cortada por el servidor"
	case errors.As(err, &certErr) && certErr.Reason == x509.Expired:
		return "certificado vencido"
	case errors.As(err, &authErr):
		return "certificado de una CA desconocida"
	case errors.As(err, &hostErr):
		return "el certificado no es para " + hostErr.Host
	case errors.As(err, &verifErr):
		return "certificado inválido: " + verifErr.Err.Error()
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		return "el servidor cerró la conexión sin responder"
	}
	// url.Error repite el método y la URL ("Get \"http://…\": …"): ya se
	// sabe cuál es, se deja solo la causa.
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Err.Error()
	}
	return err.Error()
}

// pushDetail es el detalle de un push que no llegó.
func pushDetail(interval time.Duration) string {
	return "no llegó en " + formatDuration(interval)
}

// maxPushMsg es el largo máximo, en caracteres, del mensaje de un push con
// falla: llega de afuera, sin login, y termina en la base y en un aviso.
const maxPushMsg = 200

// pushDownDetail es el detalle de un push que avisa una falla: el msg del
// script, en una línea, sin caracteres de control y recortado.
func pushDownDetail(msg string) string {
	msg = strings.Join(strings.FieldsFunc(msg, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || r == utf8.RuneError
	}), " ")
	if msg == "" {
		return "avisó una falla"
	}
	if r := []rune(msg); len(r) > maxPushMsg {
		msg = string(r[:maxPushMsg]) + "…"
	}
	return "avisó una falla: " + msg
}

// formatDuration escribe "96h", "30m", "1h30m" sin los ceros que agrega
// time.Duration.String ("96h0m0s").
func formatDuration(d time.Duration) string {
	s := d.String()
	for _, suf := range []string{"m0s", "h0m"} {
		if len(s) > len(suf) && s[len(s)-len(suf):] == suf {
			s = s[:len(s)-len(suf)+1]
		}
	}
	return s
}
