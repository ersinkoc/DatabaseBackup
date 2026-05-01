package postgres

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
)

func performMD5PasswordAuth(rw io.ReadWriter, username string, password string, authPayload []byte) error {
	if password == "" {
		return fmt.Errorf("postgres server requested MD5 password auth but no password was provided")
	}
	if len(authPayload) < 8 {
		return fmt.Errorf("postgres MD5 authentication message too short")
	}
	salt := authPayload[4:8]
	inner := md5.Sum([]byte(password + username))
	innerHex := hex.EncodeToString(inner[:])
	outerInput := append([]byte(innerHex), salt...)
	outer := md5.Sum(outerInput)
	return writeTypedMessage(rw, 'p', []byte("md5"+hex.EncodeToString(outer[:])+"\x00"))
}
