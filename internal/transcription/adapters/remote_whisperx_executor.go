package adapters

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"scriberr/internal/models"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// RemoteTransport is the exec-with-stdio seam used by the remote executor.
type RemoteTransport interface {
	Exec(ctx context.Context, command string, stdin io.Reader, stdout, stderr io.Writer) error
}

// RemoteFailure preserves the failing stage for fallback classification.
type RemoteFailure struct {
	Stage string
	Err   error
}

type remoteFailureDecision struct {
	Fallback bool
	Reason   string
}

type remoteExitStatusError interface {
	ExitStatus() int
}

func classifyRemoteFailure(err error) remoteFailureDecision {
	var remoteFailure *RemoteFailure
	if !errors.As(err, &remoteFailure) {
		return remoteFailureDecision{Reason: "remote execution failed"}
	}
	var exitError remoteExitStatusError
	if errors.As(remoteFailure.Err, &exitError) {
		if exitError.ExitStatus() == 127 {
			return remoteFailureDecision{Fallback: true, Reason: "remote wrapper not found"}
		}
		return remoteFailureDecision{Reason: fmt.Sprintf("remote wrapper exited with status %d", exitError.ExitStatus())}
	}
	if errors.Is(remoteFailure.Err, context.DeadlineExceeded) {
		return remoteFailureDecision{Fallback: true, Reason: "remote connection timed out"}
	}
	var dnsError *net.DNSError
	if errors.As(remoteFailure.Err, &dnsError) {
		return remoteFailureDecision{Fallback: true, Reason: "remote host lookup failed"}
	}
	var networkError net.Error
	if errors.As(remoteFailure.Err, &networkError) {
		if networkError.Timeout() {
			return remoteFailureDecision{Fallback: true, Reason: "remote connection timed out"}
		}
		return remoteFailureDecision{Fallback: true, Reason: "remote connection failed"}
	}
	message := strings.ToLower(remoteFailure.Err.Error())
	if strings.Contains(message, "unable to authenticate") || strings.Contains(message, "no supported methods remain") {
		return remoteFailureDecision{Fallback: true, Reason: "remote authentication failed"}
	}
	return remoteFailureDecision{Reason: "remote execution failed"}
}

func (e *RemoteFailure) Error() string { return fmt.Sprintf("remote %s failed: %v", e.Stage, e.Err) }
func (e *RemoteFailure) Unwrap() error { return e.Err }

type RemoteWhisperXExecutor struct {
	transport RemoteTransport
	profile   models.ProfileExecution
}

func NewRemoteWhisperXExecutor(transport RemoteTransport, profile models.ProfileExecution) *RemoteWhisperXExecutor {
	return &RemoteWhisperXExecutor{transport: transport, profile: profile}
}

func (e *RemoteWhisperXExecutor) Execute(ctx context.Context, jobID, audioPath string, params map[string]interface{}, outputDir, logPath string) (retErr error) {
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return &RemoteFailure{Stage: "log", Err: err}
	}
	defer logFile.Close()

	jobDir := filepath.ToSlash(filepath.Join(e.profile.RemoteWorkDir, "jobs", jobID))
	shouldCleanup := false
	defer func() {
		if !shouldCleanup {
			return
		}
		cleanupErr := e.transport.Exec(context.WithoutCancel(ctx), e.withPrefix("rm -r -- "+shellArg(jobDir)), nil, nil, logFile)
		if cleanupErr != nil && retErr == nil {
			retErr = &RemoteFailure{Stage: "cleanup", Err: cleanupErr}
		}
	}()

	audio, err := os.Open(audioPath)
	if err != nil {
		return &RemoteFailure{Stage: "submit", Err: err}
	}
	defer audio.Close()
	if err := e.transport.Exec(ctx, e.submitCommand(jobID, params), audio, logFile, logFile); err != nil {
		failure := &RemoteFailure{Stage: "submit", Err: err}
		shouldCleanup = !classifyRemoteFailure(failure).Fallback
		return failure
	}
	shouldCleanup = true

	var archive bytes.Buffer
	retrieve := e.withPrefix("tar -C " + shellArg(jobDir) + " -cf - output")
	if err := e.transport.Exec(ctx, retrieve, nil, &archive, logFile); err != nil {
		return &RemoteFailure{Stage: "retrieve", Err: err}
	}
	if err := extractOutputTar(&archive, outputDir); err != nil {
		return &RemoteFailure{Stage: "extract", Err: err}
	}
	return nil
}

func (e *RemoteWhisperXExecutor) submitCommand(jobID string, params map[string]interface{}) string {
	parts := []string{filepath.ToSlash(filepath.Join(e.profile.RemoteWorkDir, "scriberr-remote-job.sh")), "--job-id", jobID,
		"--model", parameterString(params, "model"), "--language", parameterString(params, "language")}
	if parameterBool(params, "diarize") {
		parts = append(parts, "--diarize")
	}
	if value := parameterInt(params, "min_speakers"); value > 0 {
		parts = append(parts, "--min-speakers", strconv.Itoa(value))
	}
	if value := parameterInt(params, "max_speakers"); value > 0 {
		parts = append(parts, "--max-speakers", strconv.Itoa(value))
	}
	for i := range parts {
		parts[i] = shellArg(parts[i])
	}
	return e.withPrefix(strings.Join(parts, " "))
}

func (e *RemoteWhisperXExecutor) withPrefix(command string) string {
	if prefix := strings.TrimSpace(e.profile.RemoteCommandPrefix); prefix != "" {
		return prefix + " " + command
	}
	return command
}

func parameterString(params map[string]interface{}, key string) string {
	value, _ := params[key].(string)
	return value
}
func parameterBool(params map[string]interface{}, key string) bool {
	value, _ := params[key].(bool)
	return value
}
func parameterInt(params map[string]interface{}, key string) int {
	switch value := params[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	}
	return 0
}

func shellArg(value string) string {
	safe := value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_./:-", r))
	}) == -1
	if safe {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func extractOutputTar(reader io.Reader, destination string) error {
	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if clean == "output" {
			continue
		}
		prefix := "output" + string(filepath.Separator)
		if !strings.HasPrefix(clean, prefix) {
			return fmt.Errorf("unexpected tar path %q", header.Name)
		}
		target := filepath.Join(destination, strings.TrimPrefix(clean, prefix))
		if !strings.HasPrefix(target, filepath.Clean(destination)+string(filepath.Separator)) {
			return fmt.Errorf("unsafe tar path %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode)&0777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, tr)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported tar entry %q", header.Name)
		}
	}
}

type sshTransport struct{ profile models.ProfileExecution }

func NewSSHTransport(profile models.ProfileExecution) (RemoteTransport, error) {
	return &sshTransport{profile: profile}, nil
}

func (t *sshTransport) Exec(ctx context.Context, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	key, err := os.ReadFile(t.profile.RemoteKeyPath)
	if err != nil {
		return fmt.Errorf("read SSH key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return fmt.Errorf("parse SSH key: %w", err)
	}
	hostKeyCallback, err := conventionalHostKeyCallback()
	if err != nil {
		return err
	}
	timeout := t.connectTimeout()
	port := t.profile.RemotePort
	if port == 0 {
		port = 22
	}
	config := &ssh.ClientConfig{User: t.profile.RemoteUser, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: hostKeyCallback, Timeout: timeout}
	address := net.JoinHostPort(t.profile.RemoteHost, strconv.Itoa(port))
	conn, err := t.dial(ctx, address, timeout)
	if err != nil {
		return err
	}
	cconn, channels, requests, err := ssh.NewClientConn(conn, address, config)
	if err != nil {
		conn.Close()
		return err
	}
	client := ssh.NewClient(cconn, channels, requests)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	session.Stdin, session.Stdout, session.Stderr = stdin, stdout, stderr
	done := make(chan error, 1)
	go func() { done <- session.Run(command) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		client.Close()
		<-done
		return ctx.Err()
	}
}

func (t *sshTransport) dial(ctx context.Context, address string, timeout time.Duration) (net.Conn, error) {
	return (&net.Dialer{Timeout: timeout}).DialContext(ctx, "tcp", address)
}

func (t *sshTransport) connectTimeout() time.Duration {
	timeout := time.Duration(t.profile.ConnectTimeoutSeconds) * time.Second
	if timeout <= 0 {
		return 10 * time.Second
	}
	return timeout
}

func conventionalHostKeyCallback() (ssh.HostKeyCallback, error) {
	paths := []string{"/etc/ssh/ssh_known_hosts"}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append([]string{filepath.Join(home, ".ssh", "known_hosts")}, paths...)
	}
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return knownhosts.New(path)
		}
	}
	return ssh.InsecureIgnoreHostKey(), nil
}
