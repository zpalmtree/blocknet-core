package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const maxBlockHashBatch = 1024

// Return bounded canonical hash lookups from one database snapshot. Callers
// checking many pool blocks do not need to load or serialize each full block.
func (s *APIServer) handleBlockHashes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Query().Get("heights"), ",")
	if len(parts) > maxBlockHashBatch {
		writeError(w, http.StatusBadRequest, "too many heights")
		return
	}
	heights := make([]uint64, 0, len(parts))
	for _, part := range parts {
		height, err := strconv.ParseUint(strings.TrimSpace(part), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "heights must be comma-separated unsigned integers")
			return
		}
		heights = append(heights, height)
	}
	hashes, height, err := s.daemon.Chain().Storage().CanonicalBlockHashes(heights)
	if err != nil {
		writeInternal(w, r, http.StatusInternalServerError, "unable to read block hashes", err)
		return
	}
	encoded := make(map[uint64]string, len(hashes))
	for height, hash := range hashes {
		encoded[height] = fmt.Sprintf("%x", hash)
	}
	writeJSON(w, http.StatusOK, map[string]any{"chain_height": height, "hashes": encoded})
}
