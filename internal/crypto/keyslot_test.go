package crypto

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestKeySlotFileRoundTrip(t *testing.T) {
	t.Parallel()

	rootKey := bytes.Repeat([]byte{0x42}, 32)
	now := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	file, err := NewKeySlotFile(rootKey, "ops", []byte("correct horse battery staple"), now)
	if err != nil {
		t.Fatalf("NewKeySlotFile() error = %v", err)
	}
	got, err := file.Unlock("ops", []byte("correct horse battery staple"))
	if err != nil {
		t.Fatalf("Unlock() error = %v", err)
	}
	if !bytes.Equal(got, rootKey) {
		t.Fatalf("Unlock() = %x, want %x", got, rootKey)
	}
	if _, err := file.Unlock("ops", []byte("wrong")); err == nil {
		t.Fatal("Unlock(wrong passphrase) error = nil, want error")
	}

	data, err := file.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	parsed, err := ParseKeySlotFile(data)
	if err != nil {
		t.Fatalf("ParseKeySlotFile() error = %v", err)
	}
	got, err = parsed.Unlock("ops", []byte("correct horse battery staple"))
	if err != nil {
		t.Fatalf("Unlock(parsed) error = %v", err)
	}
	if !bytes.Equal(got, rootKey) {
		t.Fatalf("Unlock(parsed) = %x, want %x", got, rootKey)
	}
	if got := RootKeyFingerprint(rootKey); got != "425ed4e4a36b30ea" {
		t.Fatalf("RootKeyFingerprint() = %s", got)
	}
}

func TestKeySlotAddAndRemove(t *testing.T) {
	t.Parallel()

	rootKey := bytes.Repeat([]byte{0x7}, 32)
	file, err := NewKeySlotFile(rootKey, "ops", []byte("old-passphrase"), time.Time{})
	if err != nil {
		t.Fatalf("NewKeySlotFile() error = %v", err)
	}
	if err := file.AddSlot(rootKey, "breakglass", []byte("new-passphrase"), time.Time{}); err != nil {
		t.Fatalf("AddSlot() error = %v", err)
	}
	if len(file.Slots) != 2 {
		t.Fatalf("len(Slots) = %d, want 2", len(file.Slots))
	}
	if err := file.RemoveSlot("ops", false); err != nil {
		t.Fatalf("RemoveSlot(ops) error = %v", err)
	}
	if len(file.Slots) != 1 || file.Slots[0].ID != "breakglass" {
		t.Fatalf("Slots after remove = %#v", file.Slots)
	}
	if err := file.RemoveSlot("breakglass", false); err == nil {
		t.Fatal("RemoveSlot(last) error = nil, want error")
	}
	if err := file.RemoveSlot("breakglass", true); err != nil {
		t.Fatalf("RemoveSlot(last, allow) error = %v", err)
	}
}

func TestKeySlotValidationErrors(t *testing.T) {
	t.Parallel()

	rootKey := bytes.Repeat([]byte{0x11}, 32)
	if _, err := NewKeySlotFile([]byte("short"), "ops", []byte("pass"), time.Time{}); err == nil {
		t.Fatal("NewKeySlotFile(short root) error = nil, want error")
	}
	var file KeySlotFile
	if err := file.AddSlot(rootKey, "", []byte("pass"), time.Time{}); err == nil {
		t.Fatal("AddSlot(empty id) error = nil, want error")
	}
	if err := file.AddSlot(rootKey, "ops", []byte("pass"), time.Time{}); err != nil {
		t.Fatalf("AddSlot(ops) error = %v", err)
	}
	if err := file.AddSlot(rootKey, "ops", []byte("pass"), time.Time{}); err == nil {
		t.Fatal("AddSlot(duplicate) error = nil, want error")
	}
	if _, err := file.Unlock("missing", []byte("pass")); err == nil {
		t.Fatal("Unlock(missing) error = nil, want error")
	}
	if err := file.RemoveSlot("", true); err == nil {
		t.Fatal("RemoveSlot(empty id) error = nil, want error")
	}
	if err := file.RemoveSlot("missing", true); err == nil {
		t.Fatal("RemoveSlot(missing) error = nil, want error")
	}
}

func TestParseKeySlotFileRejectsInvalidFiles(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		file KeySlotFile
	}{
		{name: "version", file: KeySlotFile{Version: 99, Algorithm: AlgorithmAES256GCM}},
		{name: "algorithm", file: KeySlotFile{Version: KeySlotFileVersion, Algorithm: "rot13"}},
		{name: "empty slot id", file: KeySlotFile{Version: KeySlotFileVersion, Algorithm: AlgorithmAES256GCM, Slots: []KeySlot{{}}}},
		{name: "duplicate slot", file: KeySlotFile{Version: KeySlotFileVersion, Algorithm: AlgorithmAES256GCM, Slots: []KeySlot{{ID: "ops"}, {ID: "ops"}}}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tc.file)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if _, err := ParseKeySlotFile(data); err == nil {
				t.Fatal("ParseKeySlotFile() error = nil, want error")
			}
		})
	}
	if _, err := ParseKeySlotFile([]byte(`{`)); err == nil {
		t.Fatal("ParseKeySlotFile(invalid json) error = nil, want error")
	}
}

func TestUnlockRejectsMalformedSlots(t *testing.T) {
	t.Parallel()

	valid, err := NewKeySlotFile(bytes.Repeat([]byte{0x22}, 32), "ops", []byte("pass"), time.Time{})
	if err != nil {
		t.Fatalf("NewKeySlotFile() error = %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*KeySlot)
	}{
		{name: "salt", mutate: func(slot *KeySlot) { slot.Salt = "not hex" }},
		{name: "nonce", mutate: func(slot *KeySlot) { slot.Nonce = "not hex" }},
		{name: "ciphertext", mutate: func(slot *KeySlot) { slot.Ciphertext = "not hex" }},
		{name: "kdf", mutate: func(slot *KeySlot) { slot.KDF.Memory = 0 }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			file := cloneKeySlotFile(valid)
			tc.mutate(&file.Slots[0])
			if _, err := file.Unlock("ops", []byte("pass")); err == nil {
				t.Fatal("Unlock() error = nil, want error")
			}
		})
	}
}

func cloneKeySlotFile(file KeySlotFile) KeySlotFile {
	file.Slots = append([]KeySlot(nil), file.Slots...)
	return file
}
