package ns

import (
	"crypto/sha1"
	"strings"

	"github.com/martinlindhe/base36"
)

const (
	Salt = "PhridcyunDryehorgedraflomcaInGiagyaumOfDyabsyacutNeldUd7"
)

func Ns(data string) string {
	sum := sha1.Sum([]byte(Salt + data))
	return strings.ToLower(base36.EncodeBytes(sum[:])[0:8])
}
