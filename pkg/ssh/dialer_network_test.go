package ssh

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type directTCPIPChannelOpen struct {
	HostToConnect       string
	PortToConnect       uint32
	OriginatorIPAddress string
	OriginatorPort      uint32
}

func TestDefaultDirectDial_Success(t *testing.T) {
	t.Parallel()

	serverAddr, _, stop := startTestSSHServer(t, false)
	defer stop()

	d := NewDefaultSSHDialer()
	cfg := &ssh.ClientConfig{
		User:            "vinay",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}

	client, err := d.defaultDirectDial(context.Background(), serverAddr, cfg)
	require.NoError(t, err)
	require.NotNil(t, client)
	defer client.Close()

	sess, err := client.NewSession()
	require.NoError(t, err)
	require.NoError(t, sess.Close())
}

func TestDefaultDirectDial_HandshakeFailure(t *testing.T) {
	t.Parallel()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	d := NewDefaultSSHDialer()
	_, err = d.defaultDirectDial(context.Background(), ln.Addr().String(), &ssh.ClientConfig{
		User:            "vinay",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	})
	require.Error(t, err)
}

func TestDefaultProxyDial_Success(t *testing.T) {
	t.Parallel()

	targetAddr, _, stopTarget := startTestSSHServer(t, false)
	defer stopTarget()

	jumpAddr, _, stopJump := startTestSSHServer(t, true)
	defer stopJump()

	d := NewDefaultSSHDialer()
	cfg := &ssh.ClientConfig{
		User:            "vinay",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}

	jumpClient, err := d.defaultDirectDial(context.Background(), jumpAddr, cfg)
	require.NoError(t, err)
	defer jumpClient.Close()

	proxied, err := d.defaultProxyDial(context.Background(), jumpClient, targetAddr, cfg)
	require.NoError(t, err)
	require.NotNil(t, proxied)
	defer proxied.Close()

	sess, err := proxied.NewSession()
	require.NoError(t, err)
	require.NoError(t, sess.Close())
}

func TestBuildSSHAgentAuth_Branches(t *testing.T) {
	// no t.Parallel: uses env var

	t.Setenv("SSH_AUTH_SOCK", filepath.Join(t.TempDir(), "missing.sock"))
	_, err := buildSSHAgentAuth()
	require.Error(t, err)

	sockNoKeys, stopNoKeys := startAgentSocket(t, false)
	defer stopNoKeys()
	t.Setenv("SSH_AUTH_SOCK", sockNoKeys)
	_, err = buildSSHAgentAuth()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no signers")

	sockWithKeys, stopWithKeys := startAgentSocket(t, true)
	defer stopWithKeys()
	t.Setenv("SSH_AUTH_SOCK", sockWithKeys)
	auth, err := buildSSHAgentAuth()
	require.NoError(t, err)
	require.NotNil(t, auth)
}

func startAgentSocket(t *testing.T, withKey bool) (string, func()) {
	t.Helper()

	sock := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)

	keyring := agent.NewKeyring()
	if withKey {
		pk, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		require.NoError(t, keyring.Add(agent.AddedKey{PrivateKey: pk}))
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_ = agent.ServeAgent(keyring, c)
			}(conn)
		}
	}()

	return sock, func() {
		_ = ln.Close()
		<-done
	}
}

func startTestSSHServer(t *testing.T, enableDirectTCPIP bool) (addr string, hostKey ssh.PublicKey, stop func()) {
	t.Helper()

	hostPrivate, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	hostSigner, err := ssh.NewSignerFromKey(hostPrivate)
	require.NoError(t, err)

	cfg := &ssh.ServerConfig{NoClientAuth: true}
	cfg.AddHostKey(hostSigner)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			netConn, err := ln.Accept()
			if err != nil {
				return
			}
			go handleSSHConn(netConn, cfg, enableDirectTCPIP)
		}
	}()

	return ln.Addr().String(), hostSigner.PublicKey(), func() {
		_ = ln.Close()
		<-done
	}
}

func handleSSHConn(netConn net.Conn, cfg *ssh.ServerConfig, enableDirectTCPIP bool) {
	_, chans, reqs, err := ssh.NewServerConn(netConn, cfg)
	if err != nil {
		_ = netConn.Close()
		return
	}
	go ssh.DiscardRequests(reqs)

	for ch := range chans {
		switch ch.ChannelType() {
		case "session":
			channel, requests, err := ch.Accept()
			if err != nil {
				continue
			}
			go ssh.DiscardRequests(requests)
			go func(ch ssh.Channel) {
				_, _ = io.Copy(io.Discard, ch)
				_ = ch.Close()
			}(channel)
		case "direct-tcpip":
			if !enableDirectTCPIP {
				_ = ch.Reject(ssh.Prohibited, "direct-tcpip disabled")
				continue
			}
			var msg directTCPIPChannelOpen
			if err := ssh.Unmarshal(ch.ExtraData(), &msg); err != nil {
				_ = ch.Reject(ssh.ConnectionFailed, "invalid payload")
				continue
			}
			target := net.JoinHostPort(msg.HostToConnect, strconv.Itoa(int(msg.PortToConnect)))
			targetConn, err := net.DialTimeout("tcp", target, 2*time.Second)
			if err != nil {
				_ = ch.Reject(ssh.ConnectionFailed, err.Error())
				continue
			}
			channel, requests, err := ch.Accept()
			if err != nil {
				_ = targetConn.Close()
				continue
			}
			go ssh.DiscardRequests(requests)
			go bridge(channel, targetConn)
		default:
			_ = ch.Reject(ssh.UnknownChannelType, fmt.Sprintf("unsupported channel type: %s", ch.ChannelType()))
		}
	}
}

func bridge(a ssh.Channel, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(a, b)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(b, a)
		done <- struct{}{}
	}()
	<-done
	_ = a.Close()
	_ = b.Close()
	<-done
}
