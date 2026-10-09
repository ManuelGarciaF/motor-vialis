package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestUnsafeStartupErrorIsNotLogged(t *testing.T) {
	var log bytes.Buffer
	logStartupError(slog.New(slog.NewJSONHandler(&log, nil)), errors.New("private-password"))
	if strings.Contains(log.String(), "private-password") {
		t.Fatal("arbitrary error text logged")
	}
}

func TestWorkerStartup(t *testing.T) {
	for _, test := range []struct {
		name        string
		args        []string
		credentials bool
		database    string
		wantError   bool
	}{
		{name: "help", args: []string{"-h"}},
		{name: "invalid interval", args: []string{"-interval=0"}, wantError: true},
		{name: "extra arguments", args: []string{"unexpected"}, wantError: true},
		{name: "missing credentials", database: "postgresql://private-password@invalid", wantError: true},
		{name: "invalid database", credentials: true, database: "postgresql://private-password@%invalid", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			id, secret := "", ""
			if test.credentials {
				id = "private-client"
				secret = "private-secret"
			}
			t.Setenv("GCBA_TRANSPORT_CLIENT_ID", id)
			t.Setenv("GCBA_TRANSPORT_CLIENT_SECRET", secret)
			t.Setenv("DATABASE_URL", test.database)
			var log, output bytes.Buffer
			err := run(context.Background(), test.args, slog.New(slog.NewJSONHandler(&log, nil)), &output)
			if (err != nil) != test.wantError {
				t.Fatalf("error: %v", err)
			}
			if err != nil {
				logStartupError(slog.New(slog.NewJSONHandler(&log, nil)), err)
			}
			if test.name == "missing credentials" && !strings.Contains(log.String(), "credentials_missing") {
				t.Fatal("missing safe startup diagnosis")
			}
			if test.name == "invalid database" && !strings.Contains(log.String(), "database_connection_failed") {
				t.Fatal("missing safe database diagnosis")
			}
			combined := log.String() + output.String()
			if err != nil {
				combined += err.Error()
			}
			if strings.Contains(combined, "private-") {
				t.Fatal("startup exposes credentials")
			}
		})
	}
}
