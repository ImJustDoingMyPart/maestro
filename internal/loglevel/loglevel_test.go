package loglevel

import "testing"

// Los mismos casos que web/src/logs.test.mjs.
func TestDetect(t *testing.T) {
	cases := []struct {
		s    string
		want Level
	}{
		{"ERROR: relation does not exist", Error},
		{"2026/11/10 FATAL boom", Error},
		{`time=x level=ERROR msg="falló"`, Error},
		{"t=x lvl=eror msg=x", Error},
		{`{"level":"error","msg":"x"}`, Error},
		{`{"severity": "ERROR"}`, Error},
		{"[error] 12#12: connect() failed", Error},
		{`{"level":50,"msg":"x"}`, Error},
		{"WARN deprecated", Warn},
		{`{"level":"warn"}`, Warn},
		{"level=warning msg=x", Warn},
		{`{"level":40}`, Warn},
		{"0 errors, 2 warnings", Info},
		{"error handling refactor", Info},
		{`{"level":"info","msg":"error count 0"}`, Info},
		{`{"level":30}`, Info},
		{"ERRORS=0", Info},
		{"", Info},
	}
	for _, c := range cases {
		if got := Detect(c.s); got != c.want {
			t.Errorf("Detect(%q) = %d, quería %d", c.s, got, c.want)
		}
	}
}

func TestStripANSI(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b[32mINFO\x1b[0m listo": "INFO listo",
		"\x1b[?25lnada":             "nada",
		"sin escapes":               "sin escapes",
	} {
		if got := StripANSI(in); got != want {
			t.Errorf("StripANSI(%q) = %q, quería %q", in, got, want)
		}
	}
}
