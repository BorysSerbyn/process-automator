package tray

import (
	"log"
	"os"
	"path/filepath"
	"sync"
)

// LogLimit is how big the tray's log grows before it starts again (the one
// before kept beside it, as <log>.old).
const LogLimit = 1 << 20

// LogFile is the tray's log: a file that starts again past its limit. The
// windows app the tray starts writes to it too (its stderr is the file).
type LogFile struct {
	path  string
	limit int64
	mu    sync.Mutex
	f     *os.File
}

// OpenLog opens the log at path (made if missing), started again first if
// it's past limit.
func OpenLog(path string, limit int64) (*LogFile, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	l := &LogFile{path: path, limit: limit}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.open(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *LogFile) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	l.f = f
	if st, err := f.Stat(); err == nil && st.Size() >= l.limit {
		return l.rotate()
	}
	return nil
}

// rotate keeps the log as <log>.old and starts a new one.
func (l *LogFile) rotate() error {
	_ = l.f.Close()
	_ = os.Rename(l.path, l.path+".old")
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	l.f = f
	return nil
}

// Write appends p, starting the log again first when p would take it past
// its limit (what the windows app wrote counts: its size is the file's).
func (l *LogFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if st, err := l.f.Stat(); err == nil && st.Size()+int64(len(p)) > l.limit {
		if err := l.rotate(); err != nil {
			return 0, err
		}
	}
	return l.f.Write(p)
}

// File is the log's file now, for a process the tray starts to write to.
func (l *LogFile) File() *os.File {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f
}

// logFile is the tray's log, once SetLog has opened it.
var (
	logMu   sync.Mutex
	logFile *LogFile
)

// SetLog makes l the tray's log: its messages, and what the windows it
// starts write.
func SetLog(l *LogFile) {
	logMu.Lock()
	logFile = l
	logMu.Unlock()
	log.SetOutput(l)
}

// childStderr is where a process the tray starts writes its errors: the
// log's file, or nil (none yet).
func childStderr() *os.File {
	logMu.Lock()
	defer logMu.Unlock()
	if logFile == nil {
		return nil
	}
	return logFile.File()
}
