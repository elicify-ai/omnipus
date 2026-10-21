package session

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strconv"
	"strings"
)

// HistoryPosition is a canonical opaque same-session occurrence fingerprint.
// It is neither an ordering key nor an authorization credential.
type HistoryPosition string

func ParseHistoryPosition(raw string) (HistoryPosition, error) {
	if len(raw) != 47 || !strings.HasPrefix(raw, "hp1_") {
		return "", ErrInvalidHistoryPosition
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(raw[4:])
	if err != nil || len(b) != sha256.Size || base64.RawURLEncoding.EncodeToString(b) != raw[4:] {
		return "", ErrInvalidHistoryPosition
	}
	return HistoryPosition(raw), nil
}
func historyPositionForAddress(sessionID string, addr ArchiveAddress) (HistoryPosition, error) {
	if sessionID == "" || addr.PartitionKey == "" || addr.ByteOffset < 0 {
		return "", ErrInvalidHistoryPosition
	}
	h := sha256.New()
	for _, v := range []string{sessionID, addr.PartitionKey, strconv.FormatInt(addr.ByteOffset, 10), addr.EntryID} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(v)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(v))
	}
	return HistoryPosition("hp1_" + base64.RawURLEncoding.EncodeToString(h.Sum(nil))), nil
}
