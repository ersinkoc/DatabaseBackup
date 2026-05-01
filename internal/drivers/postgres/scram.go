package postgres

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const scramSHA256Mechanism = "SCRAM-SHA-256"

var newSCRAMNonce = randomSCRAMNonce

func performSCRAMSHA256(rw io.ReadWriter, username string, password string, authPayload []byte) error {
	if password == "" {
		return fmt.Errorf("postgres server requested SCRAM-SHA-256 but no password was provided")
	}
	if !supportsSCRAMSHA256(authPayload) {
		return fmt.Errorf("postgres server did not advertise SCRAM-SHA-256")
	}
	nonce, err := newSCRAMNonce()
	if err != nil {
		return err
	}
	clientFirstBare := "n=" + escapeSCRAMUsername(username) + ",r=" + nonce
	clientFirst := "n,," + clientFirstBare
	if err := writeSASLInitialResponse(rw, scramSHA256Mechanism, []byte(clientFirst)); err != nil {
		return err
	}

	msg, err := readPGMessage(rw)
	if err != nil {
		return err
	}
	if msg.Type == 'E' {
		return parseErrorResponse(msg.Payload)
	}
	code, err := parseAuthenticationCode(msg.Payload)
	if err != nil {
		return err
	}
	if code != 11 {
		return fmt.Errorf("unexpected postgres SCRAM continue authentication code %d", code)
	}
	serverFirst := string(msg.Payload[4:])
	clientFinal, serverSignature, err := buildSCRAMSHA256Final(username, password, nonce, clientFirstBare, serverFirst)
	if err != nil {
		return err
	}
	if err := writeTypedMessage(rw, 'p', []byte(clientFinal)); err != nil {
		return err
	}

	msg, err = readPGMessage(rw)
	if err != nil {
		return err
	}
	if msg.Type == 'E' {
		return parseErrorResponse(msg.Payload)
	}
	code, err = parseAuthenticationCode(msg.Payload)
	if err != nil {
		return err
	}
	if code != 12 {
		return fmt.Errorf("unexpected postgres SCRAM final authentication code %d", code)
	}
	serverFinal := string(msg.Payload[4:])
	attrs, err := parseSCRAMAttributes(serverFinal)
	if err != nil {
		return err
	}
	if serverError := attrs['e']; serverError != "" {
		return fmt.Errorf("postgres SCRAM authentication failed: %s", serverError)
	}
	if attrs['v'] == "" {
		return fmt.Errorf("postgres SCRAM final message missing server signature")
	}
	if attrs['v'] != serverSignature {
		return fmt.Errorf("postgres SCRAM server signature mismatch")
	}
	return nil
}

func supportsSCRAMSHA256(authPayload []byte) bool {
	mechanisms := authPayload[4:]
	for len(mechanisms) > 0 {
		end := bytes.IndexByte(mechanisms, 0)
		if end < 0 {
			return false
		}
		if end == 0 {
			return false
		}
		if string(mechanisms[:end]) == scramSHA256Mechanism {
			return true
		}
		mechanisms = mechanisms[end+1:]
	}
	return false
}

func writeSASLInitialResponse(w io.Writer, mechanism string, initial []byte) error {
	var payload bytes.Buffer
	payload.WriteString(mechanism)
	payload.WriteByte(0)
	if err := binary.Write(&payload, binary.BigEndian, int32(len(initial))); err != nil {
		return err
	}
	payload.Write(initial)
	return writeTypedMessage(w, 'p', payload.Bytes())
}

func buildSCRAMSHA256Final(username string, password string, clientNonce string, clientFirstBare string, serverFirst string) (string, string, error) {
	attrs, err := parseSCRAMAttributes(serverFirst)
	if err != nil {
		return "", "", err
	}
	serverNonce := attrs['r']
	if serverNonce == "" {
		return "", "", fmt.Errorf("postgres SCRAM server-first message missing nonce")
	}
	if !strings.HasPrefix(serverNonce, clientNonce) {
		return "", "", fmt.Errorf("postgres SCRAM server nonce does not extend client nonce")
	}
	saltText := attrs['s']
	if saltText == "" {
		return "", "", fmt.Errorf("postgres SCRAM server-first message missing salt")
	}
	salt, err := base64.StdEncoding.DecodeString(saltText)
	if err != nil {
		return "", "", fmt.Errorf("decode postgres SCRAM salt: %w", err)
	}
	iterations, err := strconv.Atoi(attrs['i'])
	if err != nil || iterations <= 0 {
		return "", "", fmt.Errorf("invalid postgres SCRAM iteration count %q", attrs['i'])
	}

	clientFinalWithoutProof := "c=" + base64.StdEncoding.EncodeToString([]byte("n,,")) + ",r=" + serverNonce
	authMessage := clientFirstBare + "," + serverFirst + "," + clientFinalWithoutProof
	saltedPassword := pbkdf2.Key([]byte(password), salt, iterations, sha256.Size, sha256.New)
	clientKey := scramHMAC(saltedPassword, "Client Key")
	storedKeySum := sha256.Sum256(clientKey)
	clientSignature := scramHMAC(storedKeySum[:], authMessage)
	clientProof := xorBytes(clientKey, clientSignature)
	serverKey := scramHMAC(saltedPassword, "Server Key")
	serverSignature := scramHMAC(serverKey, authMessage)
	_ = username
	return clientFinalWithoutProof + ",p=" + base64.StdEncoding.EncodeToString(clientProof), base64.StdEncoding.EncodeToString(serverSignature), nil
}

func parseSCRAMAttributes(message string) (map[byte]string, error) {
	out := map[byte]string{}
	if message == "" {
		return out, nil
	}
	for _, part := range strings.Split(message, ",") {
		if len(part) < 3 || part[1] != '=' {
			return nil, fmt.Errorf("malformed postgres SCRAM attribute %q", part)
		}
		out[part[0]] = part[2:]
	}
	return out, nil
}

func scramHMAC(key []byte, message string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return mac.Sum(nil)
}

func xorBytes(left []byte, right []byte) []byte {
	out := make([]byte, len(left))
	for i := range left {
		out[i] = left[i] ^ right[i]
	}
	return out
}

func randomSCRAMNonce() (string, error) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(raw[:]), nil
}

func escapeSCRAMUsername(username string) string {
	username = strings.ReplaceAll(username, "=", "=3D")
	return strings.ReplaceAll(username, ",", "=2C")
}
