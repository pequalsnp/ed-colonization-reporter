package web

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/pequalsnp/ed-colonization-reporter/internal/reporter"
)

// activityFileLogger writes every reporter.Status emitted on the hub to
// a plain text file on disk. The file is opened append-only and rotated
// when it crosses a soft size cap so it doesn't grow unboundedly.
//
// One line per entry: "[ISO8601] LEVEL  message".
//
// File location:
//   - Linux: $XDG_STATE_HOME/ed-colonization-reporter/activity.log
//     (or ~/.local/state/ed-colonization-reporter/activity.log)
//   - macOS / Windows: os.UserCacheDir/ed-colonization-reporter/activity.log
//
// Subscribes once at server start; if writing fails we silently keep
// going — disk-log failures shouldn't break the running app.
type activityFileLogger struct {
	path     string
	maxBytes int64

	mu sync.Mutex
	f  *os.File
}

const defaultMaxLogBytes = 2 * 1024 * 1024 // 2 MiB

// resolveActivityLogPath returns the file the auto-logger writes to.
func resolveActivityLogPath() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "ed-colonization-reporter", "activity.log")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "ed-colonization-reporter", "activity.log")
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "ed-colonization-reporter", "activity.log")
	}
	return filepath.Join(os.TempDir(), "edcolreport-activity.log")
}

func newActivityFileLogger(path string) *activityFileLogger {
	return &activityFileLogger{path: path, maxBytes: defaultMaxLogBytes}
}

func (l *activityFileLogger) start() error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.f = f
	l.mu.Unlock()
	// Marker line so a new boot is visible in the file.
	_, _ = fmt.Fprintf(f, "\n[%s] BOOT  edcolreport started\n", time.Now().Format(time.RFC3339))
	return nil
}

func (l *activityFileLogger) write(s reporter.Status) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return
	}
	line := fmt.Sprintf("[%s] %-5s %s\n",
		s.Time.Format(time.RFC3339), s.Level.String(), s.Message)
	if _, err := l.f.WriteString(line); err != nil {
		return
	}
	l.maybeRotate()
}

// maybeRotate truncates the log if it has grown past maxBytes, keeping
// the most recent ~half. Cheap: read tail, truncate, write back. Called
// after every write so we self-trim continuously rather than building up
// a huge file and doing one big rotation.
func (l *activityFileLogger) maybeRotate() {
	info, err := l.f.Stat()
	if err != nil || info.Size() < l.maxBytes {
		return
	}
	keep := l.maxBytes / 2
	// Re-open read-only to seek.
	src, err := os.Open(l.path)
	if err != nil {
		return
	}
	defer src.Close()
	if _, err := src.Seek(-keep, io.SeekEnd); err != nil {
		return
	}
	// Advance past the next newline so the kept portion starts on a
	// clean line.
	buf := make([]byte, 1)
	for {
		n, err := src.Read(buf)
		if n == 0 || err != nil {
			break
		}
		if buf[0] == '\n' {
			break
		}
	}
	tail, err := io.ReadAll(src)
	if err != nil {
		return
	}
	if _, err := l.f.Seek(0, io.SeekStart); err != nil {
		return
	}
	if err := l.f.Truncate(0); err != nil {
		return
	}
	_, _ = l.f.Write(tail)
}

func (l *activityFileLogger) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
}
