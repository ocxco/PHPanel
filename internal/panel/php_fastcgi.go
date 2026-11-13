package panel

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

// A private local FastCGI request reports the live worker, not a new CLI process.
func (m *Manager) probeRunningPHP(i Instance, proc *managedProcess) (PHPProbe, error) {
	m.mu.Lock()
	owned := m.processes[i.ID] == proc
	m.mu.Unlock()
	if !owned {
		return PHPProbe{}, fmt.Errorf("PHP 进程已变化，请刷新")
	}
	file, err := os.CreateTemp(filepath.Join(i.Path, "temp"), "phpanel-diagnostic-*.php")
	if err != nil {
		return PHPProbe{}, err
	}
	path := file.Name()
	defer os.Remove(path)
	_, err = file.WriteString("<?php " + phpDiagnostic)
	closeErr := file.Close()
	if err != nil {
		return PHPProbe{}, err
	}
	if closeErr != nil {
		return PHPProbe{}, closeErr
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", i.Port), 2*time.Second)
	if err != nil {
		return PHPProbe{}, fmt.Errorf("无法查询运行中的 PHP: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	write := func(kind byte, body []byte) error {
		header := []byte{1, kind, 0, 1, byte(len(body) >> 8), byte(len(body)), 0, 0}
		packet := append(header, body...)
		for len(packet) > 0 {
			n, err := conn.Write(packet)
			if err != nil {
				return err
			}
			if n == 0 {
				return io.ErrShortWrite
			}
			packet = packet[n:]
		}
		return nil
	}
	if err = write(1, []byte{0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return PHPProbe{}, err
	}
	var params bytes.Buffer
	length := func(n int) {
		if n < 128 {
			params.WriteByte(byte(n))
		} else {
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], uint32(n)|0x80000000)
			params.Write(b[:])
		}
	}
	for _, pair := range [][2]string{{"GATEWAY_INTERFACE", "CGI/1.1"}, {"REQUEST_METHOD", "GET"}, {"SCRIPT_FILENAME", nativePath(path)}, {"SCRIPT_NAME", "/phpanel-diagnostic.php"}, {"REQUEST_URI", "/phpanel-diagnostic.php"}, {"SERVER_PROTOCOL", "HTTP/1.1"}, {"SERVER_NAME", "localhost"}, {"SERVER_ADDR", "127.0.0.1"}, {"SERVER_PORT", fmt.Sprint(i.Port)}, {"REMOTE_ADDR", "127.0.0.1"}, {"CONTENT_LENGTH", "0"}, {"REDIRECT_STATUS", "200"}} {
		length(len(pair[0]))
		length(len(pair[1]))
		params.WriteString(pair[0])
		params.WriteString(pair[1])
	}
	if params.Len() > 65535 {
		return PHPProbe{}, fmt.Errorf("诊断路径过长")
	}
	if err = write(4, params.Bytes()); err != nil {
		return PHPProbe{}, err
	}
	if err = write(4, nil); err != nil {
		return PHPProbe{}, err
	}
	if err = write(5, nil); err != nil {
		return PHPProbe{}, err
	}
	var output, stderr bytes.Buffer
	for {
		var header [8]byte
		if _, err = io.ReadFull(conn, header[:]); err != nil {
			return PHPProbe{}, fmt.Errorf("PHP 运行诊断失败: %w", err)
		}
		if header[0] != 1 || binary.BigEndian.Uint16(header[2:4]) != 1 {
			return PHPProbe{}, fmt.Errorf("无效的 FastCGI 响应")
		}
		n := int(binary.BigEndian.Uint16(header[4:6]))
		body := make([]byte, n+int(header[6]))
		if _, err = io.ReadFull(conn, body); err != nil {
			return PHPProbe{}, err
		}
		if output.Len()+stderr.Len()+n > 1024*1024 {
			return PHPProbe{}, fmt.Errorf("诊断响应过大")
		}
		switch header[1] {
		case 6:
			output.Write(body[:n])
		case 7:
			stderr.Write(body[:n])
		case 3:
			if n < 8 || binary.BigEndian.Uint32(body[:4]) != 0 || body[4] != 0 {
				return PHPProbe{}, fmt.Errorf("PHP 诊断退出异常: %s", stderr.String())
			}
			m.mu.Lock()
			owned = m.processes[i.ID] == proc
			m.mu.Unlock()
			if !owned {
				return PHPProbe{}, fmt.Errorf("PHP 进程已变化，请刷新")
			}
			return parsePHPProbe(output.Bytes())
		}
	}
}
