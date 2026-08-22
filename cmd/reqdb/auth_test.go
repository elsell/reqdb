package main

import (
	"os"
	"testing"
)

func TestCredentialsAreServerKeyedAndPrivate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	config := credentialFile{Servers: map[string]serverCredential{"https://one.example": {Token: "one"}, "https://two.example": {Token: "two", Project: "beta"}}}
	if err := saveCredentials(config); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Servers["https://two.example"].Project != "beta" || len(loaded.Servers) != 2 {
		t.Fatalf("credentials = %+v", loaded)
	}
	path, _ := credentialPath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}
