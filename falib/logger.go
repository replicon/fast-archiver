package falib

type Logger interface {
	Verbose(v ...interface{})
	Warning(v ...interface{})
}
