// Package sshrestore restores backup archives on a target host through SSH.
package sshrestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const defaultRestoreImage = "alpine:3.23.5"

// Config describes SSH access to a target server.
type Config struct {
	Host       string
	Port       string
	User       string
	PrivateKey []byte
	Timeout    time.Duration
}

// Executor runs restore commands on a target server.
type Executor struct {
	client *ssh.Client
}

// Dial connects to the target server.
func Dial(ctx context.Context, cfg Config) (*Executor, error) {
	if cfg.Host == "" || cfg.User == "" {
		return nil, errors.New("ssh host and user are required")
	}
	if cfg.Port == "" {
		cfg.Port = "22"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	signer, err := ssh.ParsePrivateKey(cfg.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("parse ssh private key: %w", err)
	}
	sshConfig := &ssh.ClientConfig{
		User: cfg.User,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		//nolint:gosec // v1 supports emergency restore against unmanaged hosts; host key pinning is planned config.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         cfg.Timeout,
	}
	dialer := net.Dialer{Timeout: cfg.Timeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(cfg.Host, cfg.Port))
	if err != nil {
		return nil, fmt.Errorf("dial ssh: %w", err)
	}
	clientConn, chans, reqs, err := ssh.NewClientConn(conn, net.JoinHostPort(cfg.Host, cfg.Port), sshConfig)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("handshake ssh: %w", err)
	}
	return &Executor{client: ssh.NewClient(clientConn, chans, reqs)}, nil
}

// Close closes the SSH client.
func (e *Executor) Close() error {
	return e.client.Close()
}

// Upload writes a file to a remote path using a minimal SCP protocol.
func (e *Executor) Upload(ctx context.Context, remotePath string, size int64, body io.Reader) error {
	session, err := e.client.NewSession()
	if err != nil {
		return fmt.Errorf("create upload session: %w", err)
	}
	defer session.Close()

	writer, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("open scp stdin: %w", err)
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- session.Run("scp -t " + shellQuote(path.Dir(remotePath)))
	}()

	filename := path.Base(remotePath)
	if _, err := fmt.Fprintf(writer, "C0600 %d %s\n", size, filename); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write scp header: %w", err)
	}
	if _, err := io.Copy(writer, body); err != nil {
		_ = writer.Close()
		return fmt.Errorf("copy scp body: %w", err)
	}
	if _, err := fmt.Fprint(writer, "\x00"); err != nil {
		_ = writer.Close()
		return fmt.Errorf("write scp terminator: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close scp stdin: %w", err)
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("upload cancelled: %w", ctx.Err())
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("run scp: %w", err)
		}
		return nil
	}
}

// Run executes a remote command.
func (e *Executor) Run(ctx context.Context, command string) ([]byte, error) {
	session, err := e.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create command session: %w", err)
	}
	defer session.Close()

	type result struct {
		output []byte
		err    error
	}
	ch := make(chan result, 1)
	go func() {
		output, err := session.CombinedOutput(command)
		ch <- result{output: output, err: err}
	}()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		return nil, fmt.Errorf("command cancelled: %w", ctx.Err())
	case result := <-ch:
		if result.err != nil {
			return result.output, fmt.Errorf("run remote command: %w", result.err)
		}
		return result.output, nil
	}
}

// BuildVolumeRestoreCommand returns a Docker command that extracts an archive
// into a named volume through a temporary helper container.
func BuildVolumeRestoreCommand(archivePath string, volumeName string, image string) (string, error) {
	if archivePath == "" || volumeName == "" {
		return "", errors.New("archive path and volume name are required")
	}
	if image == "" {
		image = defaultRestoreImage
	}
	innerArchivePath := shellQuote("/backup/" + path.Base(archivePath))
	command := fmt.Sprintf(
		"docker run --rm -v %s:/backup:ro -v %s:/restore %s sh -c %s",
		shellQuote(path.Dir(archivePath)),
		shellQuote(volumeName),
		shellQuote(image),
		shellQuote("tar -xzf "+innerArchivePath+" -C /restore"),
	)
	return command, nil
}

// BuildDBRestoreCommand returns a Docker command that feeds a dump to a restore container.
func BuildDBRestoreCommand(archivePath string, containerName string, restoreCommand string) (string, error) {
	if archivePath == "" || containerName == "" || restoreCommand == "" {
		return "", errors.New("archive path, container name, and restore command are required")
	}
	innerArchivePath := shellQuote("/backup/" + path.Base(archivePath))
	command := fmt.Sprintf(
		"docker run --rm --volumes-from %s -v %s:/backup:ro %s sh -c %s",
		shellQuote(containerName),
		shellQuote(path.Dir(archivePath)),
		shellQuote(defaultRestoreImage),
		shellQuote("gzip -dc "+innerArchivePath+" | "+restoreCommand),
	)
	return command, nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
