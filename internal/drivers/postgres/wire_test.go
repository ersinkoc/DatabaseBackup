package postgres

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"io"
	"strings"
	"testing"
)

func TestPGWireStartupAndSimpleQuery(t *testing.T) {
	t.Parallel()

	rw := &scriptedReadWriter{read: bytes.NewBuffer(nil)}
	writeAuthOK(rw.read)
	writeParameterStatus(rw.read, "server_version", "17.0")
	writeReady(rw.read)
	writeRowDescription(rw.read, []pgField{{Name: "answer", DataTypeOID: 23, DataTypeSize: 4}})
	writeDataRow(rw.read, []*string{stringPtr("42")})
	writeCommandComplete(rw.read, "SELECT 1")
	writeReady(rw.read)

	result, err := pgSimpleQuery(rw, map[string]string{"user": "backup", "database": "app"}, "", "select 42")
	if err != nil {
		t.Fatalf("pgSimpleQuery() error = %v", err)
	}
	if len(result.Fields) != 1 || result.Fields[0].Name != "answer" {
		t.Fatalf("fields = %#v", result.Fields)
	}
	if len(result.Rows) != 1 || len(result.Rows[0]) != 1 || result.Rows[0][0] == nil || *result.Rows[0][0] != "42" {
		t.Fatalf("rows = %#v", result.Rows)
	}
	if result.Command != "SELECT 1" {
		t.Fatalf("command = %q", result.Command)
	}

	written := rw.write.Bytes()
	if binary.BigEndian.Uint32(written[:4]) == 0 {
		t.Fatalf("startup message length not written: %x", written[:8])
	}
	if !bytes.Contains(written, []byte("database\x00app\x00")) || !bytes.Contains(written, []byte("user\x00backup\x00")) {
		t.Fatalf("startup payload = %q", written)
	}
	if !bytes.Contains(written, []byte{'Q', 0, 0, 0, 14, 's', 'e', 'l', 'e', 'c', 't', ' ', '4', '2', 0}) {
		t.Fatalf("query message not found in %x", written)
	}
}

func TestPGWireCleartextPasswordAuth(t *testing.T) {
	t.Parallel()

	rw := &scriptedReadWriter{read: bytes.NewBuffer(nil)}
	writeAuthentication(rw.read, 3)
	writeAuthOK(rw.read)
	writeReady(rw.read)
	writeCommandComplete(rw.read, "SELECT 0")
	writeReady(rw.read)

	if _, err := pgSimpleQuery(rw, map[string]string{"user": "backup"}, "secret", "select 0"); err != nil {
		t.Fatalf("pgSimpleQuery() error = %v", err)
	}
	if !bytes.Contains(rw.write.Bytes(), []byte{'p', 0, 0, 0, 11, 's', 'e', 'c', 'r', 'e', 't', 0}) {
		t.Fatalf("password message not found in %x", rw.write.Bytes())
	}
}

func TestPGWireMD5PasswordAuth(t *testing.T) {
	t.Parallel()

	rw := &scriptedReadWriter{read: bytes.NewBuffer(nil)}
	writeAuthenticationPayload(rw.read, 5, []byte{1, 2, 3, 4})
	writeAuthOK(rw.read)
	writeReady(rw.read)
	writeCommandComplete(rw.read, "SELECT 0")
	writeReady(rw.read)

	if _, err := pgSimpleQuery(rw, map[string]string{"user": "backup"}, "secret", "select 0"); err != nil {
		t.Fatalf("pgSimpleQuery(MD5) error = %v", err)
	}
	if !bytes.Contains(rw.write.Bytes(), []byte("md577ec8e0a0cf14cbf7cb0ce0827b762f1\x00")) {
		t.Fatalf("md5 password message not found in %x", rw.write.Bytes())
	}
}

func TestPGWireSCRAMSHA256Auth(t *testing.T) {
	originalNonce := newSCRAMNonce
	newSCRAMNonce = func() (string, error) { return "clientnonce", nil }
	t.Cleanup(func() { newSCRAMNonce = originalNonce })

	serverFirst := "r=clientnonceserver,s=" + base64.StdEncoding.EncodeToString([]byte("salt")) + ",i=4096"
	_, serverSignature, err := buildSCRAMSHA256Final("backup", "secret", "clientnonce", "n=backup,r=clientnonce", serverFirst)
	if err != nil {
		t.Fatalf("buildSCRAMSHA256Final() error = %v", err)
	}
	rw := &scriptedReadWriter{read: bytes.NewBuffer(nil)}
	writeAuthenticationPayload(rw.read, 10, []byte("SCRAM-SHA-256\x00\x00"))
	writeAuthenticationPayload(rw.read, 11, []byte(serverFirst))
	writeAuthenticationPayload(rw.read, 12, []byte("v="+serverSignature))
	writeAuthOK(rw.read)
	writeReady(rw.read)
	writeCommandComplete(rw.read, "SELECT 0")
	writeReady(rw.read)

	if _, err := pgSimpleQuery(rw, map[string]string{"user": "backup"}, "secret", "select 0"); err != nil {
		t.Fatalf("pgSimpleQuery(SCRAM) error = %v", err)
	}
	written := rw.write.Bytes()
	if !bytes.Contains(written, []byte("SCRAM-SHA-256\x00")) {
		t.Fatalf("SCRAM mechanism not written in %x", written)
	}
	if !bytes.Contains(written, []byte("n,,n=backup,r=clientnonce")) {
		t.Fatalf("SCRAM client-first not written in %x", written)
	}
	if !bytes.Contains(written, []byte("c=biws,r=clientnonceserver,p=")) {
		t.Fatalf("SCRAM client-final not written in %x", written)
	}
}

func TestPGWireUnsupportedAuthAndErrorResponse(t *testing.T) {
	t.Parallel()

	rw := &scriptedReadWriter{read: bytes.NewBuffer(nil)}
	writeAuthentication(rw.read, 9)
	if _, err := pgSimpleQuery(rw, map[string]string{"user": "backup"}, "", "select 1"); err == nil || !strings.Contains(err.Error(), "authentication method 9") {
		t.Fatalf("pgSimpleQuery(unsupported auth) error = %v", err)
	}

	rw = &scriptedReadWriter{read: bytes.NewBuffer(nil)}
	writeError(rw.read, "ERROR", "boom")
	if _, err := pgSimpleQuery(rw, map[string]string{"user": "backup"}, "", "select 1"); err == nil || !strings.Contains(err.Error(), "ERROR: boom") {
		t.Fatalf("pgSimpleQuery(error response) error = %v", err)
	}
}

func TestPGWireParsersRejectMalformedMessages(t *testing.T) {
	t.Parallel()

	if _, err := parseAuthenticationCode([]byte{0, 1}); err == nil {
		t.Fatal("parseAuthenticationCode(short) error = nil, want error")
	}
	if _, err := parseRowDescription([]byte{0, 1, 'n'}); err == nil {
		t.Fatal("parseRowDescription(short) error = nil, want error")
	}
	if _, err := parseDataRow([]byte{0, 1, 0, 0, 0, 10, 'x'}); err == nil {
		t.Fatal("parseDataRow(bad length) error = nil, want error")
	}
	var framed bytes.Buffer
	framed.WriteByte('Z')
	_ = binary.Write(&framed, binary.BigEndian, int32(3))
	if _, err := readPGMessage(&framed); err == nil {
		t.Fatal("readPGMessage(short length) error = nil, want error")
	}
}

type scriptedReadWriter struct {
	read  *bytes.Buffer
	write bytes.Buffer
}

func (rw *scriptedReadWriter) Read(p []byte) (int, error) {
	return rw.read.Read(p)
}

func (rw *scriptedReadWriter) Write(p []byte) (int, error) {
	return rw.write.Write(p)
}

func writeAuthOK(w io.Writer) {
	writeAuthentication(w, 0)
}

func writeAuthentication(w io.Writer, code int32) {
	writeAuthenticationPayload(w, code, nil)
}

func writeAuthenticationPayload(w io.Writer, code int32, rest []byte) {
	var payload bytes.Buffer
	_ = binary.Write(&payload, binary.BigEndian, code)
	payload.Write(rest)
	_ = writeTypedMessage(w, 'R', payload.Bytes())
}

func writeParameterStatus(w io.Writer, key string, value string) {
	_ = writeTypedMessage(w, 'S', []byte(key+"\x00"+value+"\x00"))
}

func writeReady(w io.Writer) {
	_ = writeTypedMessage(w, 'Z', []byte{'I'})
}

func writeRowDescription(w io.Writer, fields []pgField) {
	var payload bytes.Buffer
	_ = binary.Write(&payload, binary.BigEndian, int16(len(fields)))
	for _, field := range fields {
		payload.WriteString(field.Name)
		payload.WriteByte(0)
		_ = binary.Write(&payload, binary.BigEndian, field.TableOID)
		_ = binary.Write(&payload, binary.BigEndian, field.Attribute)
		_ = binary.Write(&payload, binary.BigEndian, field.DataTypeOID)
		_ = binary.Write(&payload, binary.BigEndian, field.DataTypeSize)
		_ = binary.Write(&payload, binary.BigEndian, field.TypeModifier)
		_ = binary.Write(&payload, binary.BigEndian, field.FormatCode)
	}
	_ = writeTypedMessage(w, 'T', payload.Bytes())
}

func writeDataRow(w io.Writer, values []*string) {
	var payload bytes.Buffer
	_ = binary.Write(&payload, binary.BigEndian, int16(len(values)))
	for _, value := range values {
		if value == nil {
			_ = binary.Write(&payload, binary.BigEndian, int32(-1))
			continue
		}
		_ = binary.Write(&payload, binary.BigEndian, int32(len(*value)))
		payload.WriteString(*value)
	}
	_ = writeTypedMessage(w, 'D', payload.Bytes())
}

func writeCommandComplete(w io.Writer, tag string) {
	_ = writeTypedMessage(w, 'C', []byte(tag+"\x00"))
}

func writeError(w io.Writer, severity string, message string) {
	var payload bytes.Buffer
	payload.WriteByte('S')
	payload.WriteString(severity)
	payload.WriteByte(0)
	payload.WriteByte('M')
	payload.WriteString(message)
	payload.WriteByte(0)
	payload.WriteByte(0)
	_ = writeTypedMessage(w, 'E', payload.Bytes())
}

func stringPtr(value string) *string {
	return &value
}
