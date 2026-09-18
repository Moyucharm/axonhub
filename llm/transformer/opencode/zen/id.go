package zen

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"
)

const openCodeIDAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func generateOpenCodeID(prefix string) (string, error) {
	now := uint64(time.Now().UnixMilli())*0x1000 + 1
	if prefix == "ses" {
		now = ^now
	}

	timeBytes := make([]byte, 6)
	for i := range timeBytes {
		shift := uint(40 - 8*i)
		timeBytes[i] = byte(now >> shift)
	}

	randomPart := make([]byte, 14)
	alphabetSize := big.NewInt(int64(len(openCodeIDAlphabet)))
	for i := range randomPart {
		index, err := rand.Int(rand.Reader, alphabetSize)
		if err != nil {
			return "", fmt.Errorf("generate OpenCode ID randomness: %w", err)
		}
		randomPart[i] = openCodeIDAlphabet[index.Int64()]
	}

	return fmt.Sprintf("%s_%x%s", prefix, timeBytes, randomPart), nil
}
