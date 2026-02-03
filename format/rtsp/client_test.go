package rtsp

import (
	"bufio"
	"bytes"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/ugparu/gomedia/utils/sdp"
)

type fakeConn struct {
	r *bytes.Reader
	w bytes.Buffer
}

func newFakeConn(resp string) *fakeConn {
	return &fakeConn{
		r: bytes.NewReader([]byte(resp)),
		w: bytes.Buffer{},
	}
}

func (f *fakeConn) Read(p []byte) (int, error) {
	return f.r.Read(p)
}

func (f *fakeConn) Write(p []byte) (int, error) {
	return f.w.Write(p)
}

func (f *fakeConn) Close() error {
	return nil
}

func (f *fakeConn) LocalAddr() net.Addr {
	return nil
}

func (f *fakeConn) RemoteAddr() net.Addr {
	return nil
}

func (f *fakeConn) SetDeadline(time.Time) error {
	return nil
}

func (f *fakeConn) SetReadDeadline(time.Time) error {
	return nil
}

func (f *fakeConn) SetWriteDeadline(time.Time) error {
	return nil
}

func TestRequestWithBodyWritesANNOUNCE(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\n\r\n"
	fc := newFakeConn(resp)

	c := newClient()
	c.conn = fc
	c.connRW = bufio.NewReadWriter(bufio.NewReader(fc), bufio.NewWriter(fc))
	c.control = "rtsp://example.com/stream"

	body := []byte("v=0\r\ns=gomedia\r\n")
	headers := map[string]string{
		"Content-Type":   "application/sdp",
		"Content-Length": strconv.Itoa(len(body)),
	}

	if _, err := c.request(announce, headers, c.control, body, false); err != nil {
		t.Fatalf("request returned error: %v", err)
	}

	req := fc.w.String()
	if !bytes.Contains([]byte(req), []byte("ANNOUNCE rtsp://example.com/stream RTSP/1.0\r\n")) {
		t.Fatalf("request line does not contain ANNOUNCE, got: %q", req)
	}
	if !bytes.Contains([]byte(req), []byte("Content-Type: application/sdp\r\n")) {
		t.Fatalf("missing Content-Type header, got: %q", req)
	}
	if !bytes.Contains([]byte(req), []byte("Content-Length: "+strconv.Itoa(len(body))+"\r\n")) {
		t.Fatalf("missing or wrong Content-Length header, got: %q", req)
	}
	if !bytes.Contains([]byte(req), body) {
		t.Fatalf("body not written correctly, expected %q in %q", string(body), req)
	}
}

func TestAnnounceUsesSDPGenerate(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\n\r\n"
	fc := newFakeConn(resp)

	c := newClient()
	c.conn = fc
	c.connRW = bufio.NewReadWriter(bufio.NewReader(fc), bufio.NewWriter(fc))
	c.control = "rtsp://example.com/stream"

	sess := sdp.Session{
		URI: "rtsp://example.com/stream",
	}
	medias := []sdp.Media{
		{
			AVType:      "video",
			PayloadType: 96,
		},
	}

	if err := c.announce(sess, medias); err != nil {
		t.Fatalf("announce returned error: %v", err)
	}

	req := fc.w.String()
	if !bytes.Contains([]byte(req), []byte("v=0\r\n")) {
		t.Fatalf("SDP body missing v=0 line, got: %q", req)
	}
	if !bytes.Contains([]byte(req), []byte("m=video 0 RTP/AVP 96")) {
		t.Fatalf("SDP body missing media line, got: %q", req)
	}
}

func TestRecordSendsRecordMethod(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\n\r\n"
	fc := newFakeConn(resp)

	c := newClient()
	c.conn = fc
	c.connRW = bufio.NewReadWriter(bufio.NewReader(fc), bufio.NewWriter(fc))
	c.control = "rtsp://example.com/stream"

	if err := c.record(); err != nil {
		t.Fatalf("record returned error: %v", err)
	}

	req := fc.w.String()
	if !bytes.Contains([]byte(req), []byte("RECORD rtsp://example.com/stream RTSP/1.0\r\n")) {
		t.Fatalf("request line does not contain RECORD, got: %q", req)
	}
}
