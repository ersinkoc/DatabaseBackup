package postgres

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	pgProtocolVersion30 = 196608
	maxPGMessagePayload = 64 << 20
)

type pgMessage struct {
	Type    byte
	Payload []byte
}

type pgField struct {
	Name         string
	TableOID     uint32
	Attribute    int16
	DataTypeOID  uint32
	DataTypeSize int16
	TypeModifier int32
	FormatCode   int16
}

type pgQueryResult struct {
	Fields  []pgField
	Rows    [][]*string
	Command string
}

func pgSimpleQuery(rw io.ReadWriter, params map[string]string, password string, query string) (pgQueryResult, error) {
	if err := writeStartupMessage(rw, params); err != nil {
		return pgQueryResult{}, err
	}
	if err := readStartupReady(rw, params["user"], password); err != nil {
		return pgQueryResult{}, err
	}
	if err := writeTypedMessage(rw, 'Q', []byte(query+"\x00")); err != nil {
		return pgQueryResult{}, err
	}
	return readSimpleQueryResult(rw)
}

func writeStartupMessage(w io.Writer, params map[string]string) error {
	var payload bytes.Buffer
	if err := binary.Write(&payload, binary.BigEndian, int32(pgProtocolVersion30)); err != nil {
		return err
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if key == "" {
			continue
		}
		payload.WriteString(key)
		payload.WriteByte(0)
		payload.WriteString(params[key])
		payload.WriteByte(0)
	}
	payload.WriteByte(0)

	total := int32(payload.Len() + 4)
	if err := binary.Write(w, binary.BigEndian, total); err != nil {
		return err
	}
	_, err := w.Write(payload.Bytes())
	return err
}

func writeTypedMessage(w io.Writer, typ byte, payload []byte) error {
	if _, err := w.Write([]byte{typ}); err != nil {
		return err
	}
	if err := binary.Write(w, binary.BigEndian, int32(len(payload)+4)); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

func readPGMessage(r io.Reader) (pgMessage, error) {
	var typ [1]byte
	if _, err := io.ReadFull(r, typ[:]); err != nil {
		return pgMessage{}, err
	}
	var length int32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return pgMessage{}, err
	}
	if length < 4 || length-4 > maxPGMessagePayload {
		return pgMessage{}, fmt.Errorf("invalid postgres message length %d", length)
	}
	payload := make([]byte, int(length-4))
	if _, err := io.ReadFull(r, payload); err != nil {
		return pgMessage{}, err
	}
	return pgMessage{Type: typ[0], Payload: payload}, nil
}

func readStartupReady(rw io.ReadWriter, username string, password string) error {
	for {
		msg, err := readPGMessage(rw)
		if err != nil {
			return err
		}
		switch msg.Type {
		case 'R':
			code, err := parseAuthenticationCode(msg.Payload)
			if err != nil {
				return err
			}
			switch code {
			case 0:
			case 3:
				if password == "" {
					return fmt.Errorf("postgres server requested cleartext password but none was provided")
				}
				if err := writeTypedMessage(rw, 'p', []byte(password+"\x00")); err != nil {
					return err
				}
			case 5:
				if err := performMD5PasswordAuth(rw, username, password, msg.Payload); err != nil {
					return err
				}
			case 10:
				if err := performSCRAMSHA256(rw, username, password, msg.Payload); err != nil {
					return err
				}
			default:
				return fmt.Errorf("postgres authentication method %d is not supported by native pgwire path yet", code)
			}
		case 'S', 'K', 'N':
			continue
		case 'E':
			return parseErrorResponse(msg.Payload)
		case 'Z':
			return nil
		default:
			return fmt.Errorf("unexpected postgres startup message %q", msg.Type)
		}
	}
}

func readSimpleQueryResult(r io.Reader) (pgQueryResult, error) {
	var result pgQueryResult
	for {
		msg, err := readPGMessage(r)
		if err != nil {
			return pgQueryResult{}, err
		}
		switch msg.Type {
		case 'T':
			fields, err := parseRowDescription(msg.Payload)
			if err != nil {
				return pgQueryResult{}, err
			}
			result.Fields = fields
		case 'D':
			row, err := parseDataRow(msg.Payload)
			if err != nil {
				return pgQueryResult{}, err
			}
			result.Rows = append(result.Rows, row)
		case 'C':
			result.Command = strings.TrimRight(string(msg.Payload), "\x00")
		case 'E':
			return pgQueryResult{}, parseErrorResponse(msg.Payload)
		case 'Z':
			return result, nil
		case 'S', 'N':
			continue
		default:
			return pgQueryResult{}, fmt.Errorf("unexpected postgres query message %q", msg.Type)
		}
	}
}

func parseAuthenticationCode(payload []byte) (int32, error) {
	if len(payload) < 4 {
		return 0, fmt.Errorf("postgres authentication message too short")
	}
	return int32(binary.BigEndian.Uint32(payload[:4])), nil
}

func parseRowDescription(payload []byte) ([]pgField, error) {
	reader := bytes.NewReader(payload)
	count, err := readInt16(reader)
	if err != nil {
		return nil, err
	}
	if count < 0 {
		return nil, fmt.Errorf("negative postgres field count %d", count)
	}
	fields := make([]pgField, 0, count)
	for i := 0; i < int(count); i++ {
		name, err := readCString(reader)
		if err != nil {
			return nil, fmt.Errorf("field %d name: %w", i, err)
		}
		tableOID, err := readUint32(reader)
		if err != nil {
			return nil, err
		}
		attr, err := readInt16(reader)
		if err != nil {
			return nil, err
		}
		typeOID, err := readUint32(reader)
		if err != nil {
			return nil, err
		}
		typeSize, err := readInt16(reader)
		if err != nil {
			return nil, err
		}
		typeMod, err := readInt32(reader)
		if err != nil {
			return nil, err
		}
		format, err := readInt16(reader)
		if err != nil {
			return nil, err
		}
		fields = append(fields, pgField{
			Name:         name,
			TableOID:     tableOID,
			Attribute:    attr,
			DataTypeOID:  typeOID,
			DataTypeSize: typeSize,
			TypeModifier: typeMod,
			FormatCode:   format,
		})
	}
	return fields, nil
}

func parseDataRow(payload []byte) ([]*string, error) {
	reader := bytes.NewReader(payload)
	count, err := readInt16(reader)
	if err != nil {
		return nil, err
	}
	if count < 0 {
		return nil, fmt.Errorf("negative postgres row column count %d", count)
	}
	row := make([]*string, 0, count)
	for i := 0; i < int(count); i++ {
		length, err := readInt32(reader)
		if err != nil {
			return nil, err
		}
		if length == -1 {
			row = append(row, nil)
			continue
		}
		if length < 0 || int64(length) > int64(reader.Len()) {
			return nil, fmt.Errorf("invalid postgres column length %d", length)
		}
		data := make([]byte, int(length))
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		value := string(data)
		row = append(row, &value)
	}
	return row, nil
}

func parseErrorResponse(payload []byte) error {
	fields := map[byte]string{}
	for len(payload) > 0 {
		code := payload[0]
		payload = payload[1:]
		if code == 0 {
			break
		}
		end := bytes.IndexByte(payload, 0)
		if end < 0 {
			return fmt.Errorf("malformed postgres error response")
		}
		fields[code] = string(payload[:end])
		payload = payload[end+1:]
	}
	message := fields['M']
	if message == "" {
		message = "postgres server error"
	}
	if severity := fields['S']; severity != "" {
		return fmt.Errorf("%s: %s", severity, message)
	}
	return fmt.Errorf("%s", message)
}

func readCString(reader *bytes.Reader) (string, error) {
	var out []byte
	for {
		b, err := reader.ReadByte()
		if err != nil {
			return "", err
		}
		if b == 0 {
			return string(out), nil
		}
		out = append(out, b)
	}
}

func readInt16(reader io.Reader) (int16, error) {
	var value int16
	err := binary.Read(reader, binary.BigEndian, &value)
	return value, err
}

func readInt32(reader io.Reader) (int32, error) {
	var value int32
	err := binary.Read(reader, binary.BigEndian, &value)
	return value, err
}

func readUint32(reader io.Reader) (uint32, error) {
	var value uint32
	err := binary.Read(reader, binary.BigEndian, &value)
	return value, err
}
