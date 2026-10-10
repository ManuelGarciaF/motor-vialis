package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckGTFSFilesValidatesEveryHeaderBeforeReload(t *testing.T) {
	directory := t.TempDir()
	for _, definition := range gtfsFiles {
		content := ""
		for index, name := range definition.columnNames() {
			if index > 0 {
				content += ","
			}
			content += name
		}
		content += "\n"
		if err := os.WriteFile(filepath.Join(directory, definition.FileName), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := checkGTFSFiles(directory); err != nil {
		t.Fatalf("checkGTFSFiles returned error: %v", err)
	}

	if err := os.WriteFile(filepath.Join(directory, "routes.txt"), []byte("wrong\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkGTFSFiles(directory); err == nil {
		t.Fatal("checkGTFSFiles accepted an invalid header")
	}
}
