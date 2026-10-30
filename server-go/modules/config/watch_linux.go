package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Watch tells the OnChange function about edits made to the config file
// outside this module -- an operator's editor, a restored backup -- until ctx
// ends. The kernel says when the file changes (inotify), so nothing polls it.
// The module's own writes are seen too; the caller drops a version it has
// already announced.
func (s *Store) Watch(ctx context.Context) error {
	dir, name := filepath.Dir(s.path), filepath.Base(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	notify, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return err
	}
	defer unix.Close(notify)
	// Watch the directory, not the file: an editor that writes a temporary
	// file and renames it over the original replaces the inode a file watch
	// would hold, and the module itself writes by rename.
	if _, err := unix.InotifyAddWatch(notify, dir,
		unix.IN_CLOSE_WRITE|unix.IN_MOVED_TO|unix.IN_CREATE|unix.IN_DELETE); err != nil {
		return err
	}
	stop, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		return err
	}
	defer unix.Close(stop)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			var one [8]byte
			one[0] = 1
			_, _ = unix.Write(stop, one[:])
		case <-done:
		}
	}()

	buffer := make([]byte, 64*(unix.SizeofInotifyEvent+unix.NAME_MAX+1))
	for {
		fds := []unix.PollFd{{Fd: int32(notify), Events: unix.POLLIN},
			{Fd: int32(stop), Events: unix.POLLIN}}
		if _, err := unix.Poll(fds, -1); err != nil && !errors.Is(err, unix.EINTR) {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		n, err := unix.Read(notify, buffer)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if touched(buffer[:n], name) {
			s.announceCurrent()
		}
	}
}

// touched reports whether any event in a read of inotify events names file.
func touched(events []byte, file string) bool {
	for offset := 0; offset+unix.SizeofInotifyEvent <= len(events); {
		event := (*unix.InotifyEvent)(unsafe.Pointer(&events[offset]))
		start := offset + unix.SizeofInotifyEvent
		end := start + int(event.Len)
		if end > len(events) {
			return false
		}
		raw := events[start:end]
		for i, b := range raw {
			if b == 0 {
				raw = raw[:i]
				break
			}
		}
		if string(raw) == file {
			return true
		}
		offset = end
	}
	return false
}

// announceCurrent tells OnChange the version now on disk.
func (s *Store) announceCurrent() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if root, err := s.read(); err == nil {
		s.announceLocked(configNodeVersion(root))
	}
}
