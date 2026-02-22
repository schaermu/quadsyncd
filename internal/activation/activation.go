package activation

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

// Listeners returns the systemd-activated listeners.
// It checks for systemd socket activation via LISTEN_PID and LISTEN_FDS environment variables.
// Returns nil if no socket activation is detected or if the activation is not for this process.
func Listeners() ([]net.Listener, error) {
	// Check if LISTEN_PID is set and matches our process ID
	pidStr := os.Getenv("LISTEN_PID")
	if pidStr == "" {
		return nil, nil
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return nil, fmt.Errorf("invalid LISTEN_PID %q: %w", pidStr, err)
	}

	if pid != os.Getpid() {
		// Socket activation is for a different process
		return nil, nil
	}

	// Check LISTEN_FDS
	fdsStr := os.Getenv("LISTEN_FDS")
	if fdsStr == "" {
		return nil, nil
	}

	numFDs, err := strconv.Atoi(fdsStr)
	if err != nil {
		return nil, fmt.Errorf("invalid LISTEN_FDS %q: %w", fdsStr, err)
	}

	if numFDs < 1 {
		return nil, nil
	}

	// Systemd passes file descriptors starting at fd 3
	// (0=stdin, 1=stdout, 2=stderr)
	const firstFD = 3

	listeners := make([]net.Listener, 0, numFDs)
	for i := 0; i < numFDs; i++ {
		fd := firstFD + i
		file := os.NewFile(uintptr(fd), fmt.Sprintf("systemd-socket-%d", i))
		if file == nil {
			return nil, fmt.Errorf("failed to create file for fd %d", fd)
		}

		listener, err := net.FileListener(file)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("failed to create listener from fd %d: %w", fd, err)
		}

		// Close the file descriptor (listener takes ownership)
		_ = file.Close()

		listeners = append(listeners, listener)
	}

	// Unset the environment variables so child processes don't inherit them
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDS")
	_ = os.Unsetenv("LISTEN_FDNAMES")

	return listeners, nil
}
