package execenv

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type archiveContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader archiveContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}

func writeEnvironmentArchivePayload(ctx context.Context, source, staging *os.Root, excluded []string, manifest *EnvironmentArchive) error {
	file, err := staging.OpenFile("environment.tar.gz", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	digest := sha256.New()
	compressed, err := gzip.NewWriterLevel(io.MultiWriter(file, digest), gzip.BestSpeed)
	if err != nil {
		return err
	}
	w := tar.NewWriter(compressed)
	writeTree := func(root *os.Root, walkRoot, prefix string) error {
		return fs.WalkDir(root.FS(), walkRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if errors.Is(walkErr, os.ErrNotExist) && path == walkRoot {
				return nil
			}
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if prefix == "environment" {
				skip := entry.Name() == ".git" || path == envRootLockFile
				for _, cache := range excluded {
					if path == filepath.ToSlash(cache) || strings.HasPrefix(path, filepath.ToSlash(cache)+"/") {
						skip = true
						break
					}
				}
				if skip {
					if entry.IsDir() {
						return fs.SkipDir
					}
					return nil
				}
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			link := ""
			if info.Mode()&os.ModeSymlink != 0 {
				link, err = root.Readlink(filepath.FromSlash(path))
				if err != nil {
					return err
				}
			} else if !info.IsDir() && !info.Mode().IsRegular() {
				return fmt.Errorf("unsupported file type in environment: %s", path)
			}
			header, err := tar.FileInfoHeader(info, link)
			if err != nil {
				return err
			}
			header.Name = path
			if prefix != "" {
				header.Name = prefix
				if path != "." {
					header.Name += "/" + path
				}
			}
			if err := w.WriteHeader(header); err != nil {
				return err
			}
			manifest.EntryCount++
			if !info.Mode().IsRegular() {
				return nil
			}
			input, err := root.Open(filepath.FromSlash(path))
			if err != nil {
				return err
			}
			opened, err := input.Stat()
			if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
				input.Close()
				return errors.New("environment file identity changed")
			}
			count, copyErr := io.Copy(w, archiveContextReader{ctx, input})
			current, statErr := input.Stat()
			closeErr := input.Close()
			if err := errors.Join(copyErr, statErr, closeErr); err != nil {
				return err
			}
			if count != info.Size() || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
				return errors.New("environment file changed during archive")
			}
			manifest.LogicalBytes += count
			return nil
		})
	}
	writeErr := writeTree(source, ".", "environment")
	if writeErr == nil {
		writeErr = writeTree(staging, "git", "")
	}
	if err := errors.Join(writeErr, w.Close(), compressed.Close(), file.Sync()); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	manifest.PayloadBytes = info.Size()
	manifest.PayloadSHA = hex.EncodeToString(digest.Sum(nil))
	return nil
}

func validEnvironmentPayloadPath(name string) bool {
	if name != "environment" && name != "git" && !strings.HasPrefix(name, "environment/") && !strings.HasPrefix(name, "git/") {
		return false
	}
	if !filepath.IsLocal(filepath.FromSlash(name)) || filepath.ToSlash(filepath.Clean(filepath.FromSlash(name))) != strings.TrimSuffix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".git" || part == "" || part == envRootLockFile {
			return false
		}
	}
	return true
}

func verifyEnvironmentArchiveRoot(ctx context.Context, root *os.Root, manifest EnvironmentArchive) error {
	info, err := root.Lstat("environment.tar.gz")
	if err != nil || !info.Mode().IsRegular() || info.Size() != manifest.PayloadBytes {
		return errors.New("archive payload unavailable")
	}
	file, err := root.Open("environment.tar.gz")
	if err != nil {
		return err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, archiveContextReader{ctx, file}); err != nil {
		return err
	}
	if hex.EncodeToString(digest.Sum(nil)) != manifest.PayloadSHA {
		return errors.New("archive payload integrity check failed")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer compressed.Close()
	reader := tar.NewReader(archiveContextReader{ctx, compressed})
	seen := make(map[string]byte)
	var bytes int64
	count := 0
	ownerVerified := false
	scopeVerified := manifest.ReuseScope == nil
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if !validEnvironmentPayloadPath(header.Name) || header.Size < 0 || seen[header.Name] != 0 {
			return errors.New("invalid archive entry path")
		}
		switch header.Typeflag {
		case tar.TypeDir, tar.TypeSymlink:
			if header.Size != 0 {
				return errors.New("invalid archive metadata entry")
			}
		case tar.TypeReg:
		default:
			return errors.New("unsupported archive entry")
		}
		seen[header.Name] = header.Typeflag
		count++
		if count > manifest.EntryCount || header.Size > manifest.LogicalBytes-bytes {
			return errors.New("archive contents exceed manifest")
		}
		if header.Name == "environment/"+envRootOwnerFile {
			if header.Typeflag != tar.TypeReg || header.Size > 64<<10 {
				return errors.New("invalid archived owner")
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				return err
			}
			var owner EnvRootOwner
			if json.Unmarshal(data, &owner) != nil || owner != manifest.Owner {
				return errors.New("archive owner does not match manifest")
			}
			ownerVerified = true
		} else if manifest.ReuseScope != nil && header.Name == "environment/"+managedEnvProvenanceFile {
			if header.Typeflag != tar.TypeReg || header.Size > 64<<10 {
				return errors.New("invalid archived reuse scope")
			}
			data, err := io.ReadAll(reader)
			if err != nil {
				return err
			}
			var scope ManagedEnvProvenance
			if json.Unmarshal(data, &scope) != nil || scope != *manifest.ReuseScope {
				return errors.New("archive reuse scope does not match payload")
			}
			scopeVerified = true
		} else if _, err := io.Copy(io.Discard, reader); err != nil {
			return err
		}
		bytes += header.Size
	}
	if _, err := io.Copy(io.Discard, compressed); err != nil {
		return err
	}
	if count != manifest.EntryCount || bytes != manifest.LogicalBytes || !ownerVerified || !scopeVerified {
		return errors.New("archive manifest does not match payload")
	}
	return nil
}
