package main

type logger interface {
	Printf(string, ...any)
}

type headLogger struct {
	head string
	l    logger
}

func (h *headLogger) Printf(f string, args ...any) {
	h.l.Printf(h.head+f, args...)
}

type noopLogger struct {
}

func (noopLogger) Printf(f string, args ...any) {
	return
}
