package wecom

// media_stream.go — an attachment that never has to fit in memory.
//
// The buffered path in media_download.go holds each attachment twice: the
// whole ciphertext from the download, then the whole plaintext from the
// decrypt, both live at once while the upload runs. The engine caps concurrent
// media resolutions at eight, and the resource cap is 100 MiB, so the worst
// case a perfectly ordinary workspace can reach — four people sending two
// large files each — is eight times two hundred megabytes of live heap. On a
// self-hosted box that is an OOM, and the process it kills is serving Lark,
// Slack and DingTalk as well. That path remains only as the fallback for a
// storage backend without UploadStream; both shipped backends have one, so
// this file is what a real deployment runs.
//
// One temp file instead, and the ciphertext never reaches disk at all: it
// streams from the socket, decrypts block by block into the file, and the
// upload reads back from there. Peak heap per attachment becomes one buffer,
// and the number that would otherwise multiply is bounded by disk.
//
// The temp file is also what makes the streaming upload possible at all.
// S3Storage.UploadStream requires an exact ContentLength, and the plaintext
// length is the ciphertext length minus a pad that is only known after the
// final block is read — unknowable at the moment the upload must start. A
// file on disk has already answered the question: its size IS the length.
//
// Files are created 0600 and removed on every path. They hold decrypted
// attachment content, so their permissions are part of the feature rather
// than housekeeping.
//
// Where the unlink happens depends on the platform. POSIX lets us unlink a
// file that is still open, which is the strongest version: the bytes are
// already unreachable while the upload reads them, even if the process is
// killed mid-flight. Windows refuses — "The process cannot access the file
// because it is being used by another process" — so there the file is removed
// when the handle closes instead. Same end state, one syscall later.

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// mediaStreamChunk is how much ciphertext is decrypted at a time. Large
// enough that the syscall overhead disappears against a 100 MiB file, small
// enough that the buffers stay incidental next to everything else the process
// is holding.
const mediaStreamChunk = 256 << 10

// mediaTempFile is the file decryptToFile hands back. Close is the only
// cleanup the caller owes: it closes the handle and, on platforms that could
// not unlink an open file, removes the path afterwards. Calling Close twice is
// harmless.
type mediaTempFile struct {
	*os.File
	removeOnClose bool
	closeOnce     sync.Once
	closeErr      error
}

func (f *mediaTempFile) Close() error {
	f.closeOnce.Do(func() {
		f.closeErr = f.File.Close()
		if f.removeOnClose {
			if err := os.Remove(f.Name()); err != nil && !errors.Is(err, os.ErrNotExist) && f.closeErr == nil {
				f.closeErr = err
			}
		}
	})
	return f.closeErr
}

// decryptToFile reads ciphertext from src, decrypts it into a new temp file,
// and returns the file positioned at its start along with the plaintext
// length. The caller closes and removes it.
//
// CBC needs the tail before the head can be trusted — the pad is on the last
// block — so the final block is held back until the source is exhausted and
// unpadded then. Everything before it is written as it goes.
func decryptToFile(aesKey string, src io.Reader, dir string) (f *mediaTempFile, size int64, err error) {
	key, err := decodeMediaAESKey(aesKey)
	if err != nil {
		return nil, 0, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, 0, fmt.Errorf("wecom: media cipher: %w", err)
	}
	// The IV is the first 16 bytes of the key, as WeCom's own libraries do.
	mode := cipher.NewCBCDecrypter(block, key[:aes.BlockSize])

	out, err := os.CreateTemp(dir, "wecom-media-*.bin")
	if err != nil {
		return nil, 0, fmt.Errorf("wecom: media temp file: %w", err)
	}
	// Unlink eagerly where the platform allows it: the file stays readable
	// through the handle and disappears the moment the process lets go of it,
	// including on a crash. Windows cannot unlink a file it still has open, so
	// there the removal waits for Close — see mediaTempFile.
	removeOnClose := false
	if err := os.Remove(out.Name()); err != nil {
		removeOnClose = true
	}
	if err := out.Chmod(0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
		// Best effort: the file is already unlinked, so this is belt as well
		// as braces.
		_ = err
	}
	temp := &mediaTempFile{File: out, removeOnClose: removeOnClose}
	f = temp
	defer func() {
		if err != nil {
			// The error returns above clear the named result, so clean up
			// through the local handle rather than through f.
			temp.Close()
		}
	}()

	buf := make([]byte, mediaStreamChunk)
	// tail holds back the last mediaPadBlock bytes of plaintext, because the
	// PKCS#7 pad is up to 32 bytes and therefore spans TWO AES blocks — the
	// pad block and the cipher block are not the same size here, which is the
	// trap media_crypt.go documents. Holding one AES block back would unpad
	// correctly only for pads of 16 or less, i.e. about half of all files.
	tail := make([]byte, 0, mediaPadBlock+aes.BlockSize)
	var carry []byte // ciphertext bytes not yet on a block boundary
	var written int64

	emit := func(plain []byte) error {
		tail = append(tail, plain...)
		if len(tail) <= mediaPadBlock {
			return nil
		}
		cut := len(tail) - mediaPadBlock
		if _, werr := out.Write(tail[:cut]); werr != nil {
			return werr
		}
		written += int64(cut)
		tail = append(tail[:0], tail[cut:]...)
		return nil
	}

	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			carry = append(carry, buf[:n]...)
			usable := len(carry) - len(carry)%aes.BlockSize
			if usable > 0 {
				chunk := carry[:usable]
				mode.CryptBlocks(chunk, chunk)
				if err = emit(chunk); err != nil {
					return nil, 0, fmt.Errorf("wecom: media decrypt: write: %w", err)
				}
				carry = append(carry[:0], carry[usable:]...)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, 0, fmt.Errorf("wecom: media decrypt: read: %w", readErr)
		}
	}

	if len(carry) != 0 {
		return nil, 0, fmt.Errorf("wecom: media ciphertext is not a multiple of the block size (%d trailing bytes)", len(carry))
	}
	if written == 0 && len(tail) == 0 {
		return nil, 0, errors.New("wecom: media ciphertext is empty")
	}

	// The pad is inside what was held back, and unpadMedia validates every
	// byte of it — a tail that does not check out means a wrong key or a
	// truncated body, and the file in front of it cannot be trusted either.
	unpadded, err := unpadMedia(tail)
	if err != nil {
		return nil, 0, err
	}
	if len(unpadded) > 0 {
		if _, err = out.Write(unpadded); err != nil {
			return nil, 0, fmt.Errorf("wecom: media decrypt: write: %w", err)
		}
		written += int64(len(unpadded))
	}
	if _, err = out.Seek(0, io.SeekStart); err != nil {
		return nil, 0, fmt.Errorf("wecom: media decrypt: rewind: %w", err)
	}
	return f, written, nil
}

// peekFile reads up to n bytes from the head of f and rewinds it, so a caller
// that needs to sniff a content type does not have to hold the whole file.
func peekFile(f io.ReadSeeker, n int) ([]byte, error) {
	head := make([]byte, n)
	read, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return head[:read], nil
}
