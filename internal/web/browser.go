package web

import (
	"os/exec"
	"runtime"
)

// OpenBrowser tries to launch the user's default browser pointed at url. It
// returns nil if the browser was launched (or at least, the launcher was
// invoked successfully); errors mean the user needs to open the URL
// themselves and the caller should surface that.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux", "freebsd", "netbsd", "openbsd":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// "start" is a cmd.exe builtin, not a standalone executable, so we
		// invoke it via cmd /c. The empty quoted string is the window title
		// (start treats the first quoted arg as the title).
		cmd = exec.Command("cmd", "/c", "start", "", url)
	default:
		return nil // no-op on unknown OS — caller will print the URL anyway.
	}
	return cmd.Start()
}
