package localreview

import "sync"

const patchMemoCapacity = 256

type patchMemoKey struct {
	Root, OldMode, NewMode string
	Old, New               BlobRef
	HasOld, HasNew         bool
}

// This process-local index contains only trusted references produced by Git,
// not diff bytes or untrusted on-disk mappings. Disk blobs retain the task cache's
// quota/GC policy; a missing blob is regenerated on the next request.
type patchMemo struct {
	mu      sync.Mutex
	entries map[patchMemoKey]BlobRef
	order   []patchMemoKey
}

var generatedPatches patchMemo

func (s *BlobStore) patchKey(file VersionFile) patchMemoKey {
	key := patchMemoKey{Root: s.root.Name(), OldMode: file.OldMode, NewMode: file.NewMode, HasOld: file.Old != nil, HasNew: file.New != nil}
	if file.Old != nil {
		key.Old = *file.Old
	}
	if file.New != nil {
		key.New = *file.New
	}
	return key
}

func (memo *patchMemo) get(key patchMemoKey) (BlobRef, bool) {
	memo.mu.Lock()
	defer memo.mu.Unlock()
	blob, ok := memo.entries[key]
	return blob, ok
}

func (memo *patchMemo) put(key patchMemoKey, blob BlobRef) {
	memo.mu.Lock()
	defer memo.mu.Unlock()
	if memo.entries == nil {
		memo.entries = make(map[patchMemoKey]BlobRef)
	}
	if _, exists := memo.entries[key]; !exists {
		if len(memo.order) == patchMemoCapacity {
			delete(memo.entries, memo.order[0])
			memo.order = memo.order[1:]
		}
		memo.order = append(memo.order, key)
	}
	memo.entries[key] = blob
}
