//go:build !unix

package storage

// flockFile is a no-op on platforms without BSD advisory locks; the
// single-instance guard is best-effort there.
func flockFile(path string) (*fileLock, error) {
	return &fileLock{}, nil
}
