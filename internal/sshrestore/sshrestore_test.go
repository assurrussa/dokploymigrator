package sshrestore

import (
	"strings"
	"testing"
)

func TestShellQuote(t *testing.T) {
	got := shellQuote("a'b")
	if got != `'a'"'"'b'` {
		t.Fatalf("shellQuote() = %s", got)
	}
}

func TestBuildVolumeRestoreCommand(t *testing.T) {
	command, err := BuildVolumeRestoreCommand("/tmp/backups/app.tar.gz", "dokploy_volume", "")
	if err != nil {
		t.Fatalf("BuildVolumeRestoreCommand() error = %v", err)
	}
	wantScript := shellQuote("tar -xzf " + shellQuote("/backup/app.tar.gz") + " -C /restore")
	for _, part := range []string{"docker run --rm", defaultRestoreImage, wantScript} {
		if !strings.Contains(command, part) {
			t.Fatalf("command %q missing %q", command, part)
		}
	}
}

func TestBuildDBRestoreCommand(t *testing.T) {
	command, err := BuildDBRestoreCommand("/tmp/db.sql.gz", "postgres-container", "psql -U app")
	if err != nil {
		t.Fatalf("BuildDBRestoreCommand() error = %v", err)
	}
	if !strings.Contains(command, defaultRestoreImage) {
		t.Fatalf("command %q missing restore image %q", command, defaultRestoreImage)
	}
	wantScript := shellQuote("gzip -dc " + shellQuote("/backup/db.sql.gz") + " | psql -U app")
	if !strings.Contains(command, wantScript) {
		t.Fatalf("unexpected command %q", command)
	}
}

func TestRestoreCommandsQuoteArchiveNameInsideShell(t *testing.T) {
	archive := "/tmp/backups/db backup'$(touch pwn).sql.gz"

	volumeCommand, err := BuildVolumeRestoreCommand(archive, "dokploy_volume", "")
	if err != nil {
		t.Fatalf("BuildVolumeRestoreCommand() error = %v", err)
	}
	wantArchive := shellQuote("/backup/" + "db backup'$(touch pwn).sql.gz")
	wantVolumeScript := shellQuote("tar -xzf " + wantArchive + " -C /restore")
	if !strings.Contains(volumeCommand, wantVolumeScript) {
		t.Fatalf("volume command %q missing quoted inner archive %q", volumeCommand, wantArchive)
	}

	dbCommand, err := BuildDBRestoreCommand(archive, "postgres-container", "psql -U app")
	if err != nil {
		t.Fatalf("BuildDBRestoreCommand() error = %v", err)
	}
	wantDBScript := shellQuote("gzip -dc " + wantArchive + " | psql -U app")
	if !strings.Contains(dbCommand, wantDBScript) {
		t.Fatalf("db command %q missing quoted inner archive %q", dbCommand, wantArchive)
	}
}
