package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const oauthCredentialLimit = 1 << 20

var errOAuthStore = errors.New("OAuth credential store unavailable or unsafe")

type oauthCredential struct {
	Endpoint     string    `json:"endpoint"`
	Issuer       string    `json:"issuer"`
	Resource     string    `json:"resource"`
	ClientID     string    `json:"client_id"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// withOAuthCredential holds the lock across reading, refreshing and saving, so
// rotating refresh tokens cannot be consumed concurrently by separate sessions.
// Callback errors are returned unchanged; callers must keep them secret-free.
func withOAuthCredential(ctx context.Context, dir, endpoint, issuer, resource string, fn func(*oauthCredential) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if dir == "" || fn == nil {
		return errOAuthStore
	}
	directory, err := openOAuthDirectory(dir)
	if err != nil {
		return errOAuthStore
	}
	defer directory.Close()
	dirfd := int(directory.Fd())
	digest := sha256.Sum256([]byte(endpoint + "\x00" + issuer + "\x00" + resource))
	base := hex.EncodeToString(digest[:])
	lock, err := openOAuthFile(dirfd, base+".lock", unix.O_RDWR|unix.O_CREAT)
	if err != nil {
		return errOAuthStore
	}
	defer lock.Close()
	if err := lockOAuthCredential(ctx, int(lock.Fd())); err != nil {
		return err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)

	credential := oauthCredential{Endpoint: endpoint, Issuer: issuer, Resource: resource}
	file, err := openOAuthFile(dirfd, base+".json", unix.O_RDONLY)
	if err == nil {
		credential = oauthCredential{}
		data, readErr := io.ReadAll(io.LimitReader(file, oauthCredentialLimit+1))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(data) > oauthCredentialLimit || json.Unmarshal(data, &credential) != nil {
			return errOAuthStore
		}
		if credential.Endpoint != endpoint || credential.Issuer != issuer || credential.Resource != resource {
			return errOAuthStore
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return errOAuthStore
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fn(&credential); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if credential.Endpoint != endpoint || credential.Issuer != issuer || credential.Resource != resource {
		return errOAuthStore
	}
	return saveOAuthCredential(dirfd, base+".json", &credential)
}

// Pin the final directory without following a symlink. Operations below use
// only this descriptor, not the original path, even if its ancestors move.
func openOAuthDirectory(dir string) (*os.File, error) {
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errOAuthStore
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, errOAuthStore
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&07777 != 0700 || stat.Uid != uint32(os.Geteuid()) {
		unix.Close(fd)
		return nil, errOAuthStore
	}
	return os.NewFile(uintptr(fd), "oauth-store"), nil
}

// O_NONBLOCK prevents opening a malicious FIFO from hanging before Fstat.
// Reject hard links too: neither lock identity nor credentials should alias a
// file outside this private directory.
func openOAuthFile(dirfd int, name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(dirfd, name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if unix.Fstat(fd, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&07777 != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		unix.Close(fd)
		return nil, errOAuthStore
	}
	return os.NewFile(uintptr(fd), "oauth-credential"), nil
}

func lockOAuthCredential(ctx context.Context, fd int) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			return errOAuthStore
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func saveOAuthCredential(dirfd int, name string, credential *oauthCredential) error {
	data, err := json.Marshal(credential)
	if err != nil || len(data) > oauthCredentialLimit {
		return errOAuthStore
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return errOAuthStore
	}
	temporary := ".oauth-" + hex.EncodeToString(random[:]) + ".tmp"
	file, err := openOAuthFile(dirfd, temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return errOAuthStore
	}
	defer unix.Unlinkat(dirfd, temporary, 0)
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return errOAuthStore
	}
	if unix.Renameat(dirfd, temporary, dirfd, name) != nil || unix.Fsync(dirfd) != nil {
		return errOAuthStore
	}
	return nil
}
