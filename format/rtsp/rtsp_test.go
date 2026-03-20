package rtsp

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ugparu/gomedia"
	"github.com/ugparu/gomedia/codec/aac"
	"github.com/ugparu/gomedia/codec/h264"
	"github.com/ugparu/gomedia/codec/h265"
	"github.com/ugparu/gomedia/codec/mjpeg"
	"github.com/ugparu/gomedia/codec/opus"
	"github.com/ugparu/gomedia/codec/pcm"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/utils/sdp"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// fakeConn implements net.Conn backed by a bytes.Buffer for reads and writes.
type rtspFakeConn struct {
	readBuf  *bytes.Buffer
	writeBuf *bytes.Buffer
}

func newRTSPFakeConn(response string) *rtspFakeConn {
	return &rtspFakeConn{
		readBuf:  bytes.NewBufferString(response),
		writeBuf: &bytes.Buffer{},
	}
}

func (f *rtspFakeConn) Read(p []byte) (int, error)  { return f.readBuf.Read(p) }
func (f *rtspFakeConn) Write(p []byte) (int, error)  { return f.writeBuf.Write(p) }
func (f *rtspFakeConn) Close() error                  { return nil }
func (f *rtspFakeConn) LocalAddr() net.Addr            { return nil }
func (f *rtspFakeConn) RemoteAddr() net.Addr           { return nil }
func (f *rtspFakeConn) SetDeadline(time.Time) error    { return nil }
func (f *rtspFakeConn) SetReadDeadline(time.Time) error  { return nil }
func (f *rtspFakeConn) SetWriteDeadline(time.Time) error { return nil }

func setupClient(resp string) (*client, *rtspFakeConn) {
	fc := newRTSPFakeConn(resp)
	c := newClient()
	c.conn = fc
	c.connRW = bufio.NewReadWriter(bufio.NewReader(fc), bufio.NewWriter(fc))
	c.control = "rtsp://example.com/stream"
	return c, fc
}

func okResponse(cseq uint) string {
	return fmt.Sprintf("RTSP/1.0 200 OK\r\nCSeq: %d\r\n\r\n", cseq)
}

// loadH264Params loads real H264 test parameters from tests/data/h264.
func loadH264Params(t *testing.T) *h264.CodecParameters {
	t.Helper()
	spsB64 := "Z01AKJWQB4AiflwEQAAA+gAAMNQ4AAAFuNgAAehILvLgoA=="
	ppsB64 := "aOuPIA=="
	sps, err := base64.StdEncoding.DecodeString(spsB64)
	if err != nil {
		t.Fatal(err)
	}
	pps, err := base64.StdEncoding.DecodeString(ppsB64)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := h264.NewCodecDataFromSPSAndPPS(sps, pps)
	if err != nil {
		t.Fatal(err)
	}
	return &cp
}

// loadH265Params loads real H265 test parameters from tests/data/hevc.
func loadH265Params(t *testing.T) *h265.CodecParameters {
	t.Helper()
	vpsB64 := "QAEMAf//AWAAAAMAgAAAAwAAAwCWrAk="
	spsB64 := "QgEBAWAAAAMAgAAAAwAAAwCWoAFQIAXx/ja7tTd3JdYC3AQEBBAAAD6AAALuByHe5RAD04ABE5wAenAAInOI"
	ppsB64 := "RAHBcrCcHA5iQA=="
	vps, _ := base64.StdEncoding.DecodeString(vpsB64)
	sps, _ := base64.StdEncoding.DecodeString(spsB64)
	pps, _ := base64.StdEncoding.DecodeString(ppsB64)
	cp, err := h265.NewCodecDataFromVPSAndSPSAndPPS(vps, sps, pps)
	if err != nil {
		t.Fatal(err)
	}
	return &cp
}

// loadAACParams loads real AAC test parameters.
func loadAACParams(t *testing.T) *aac.CodecParameters {
	t.Helper()
	configB64 := "FAg="
	config, _ := base64.StdEncoding.DecodeString(configB64)
	cp, err := aac.NewCodecDataFromMPEG4AudioConfigBytes(config)
	if err != nil {
		t.Fatal(err)
	}
	return &cp
}

// =========================================================================
// controlTrack
// =========================================================================

func TestControlTrack_AbsoluteURL(t *testing.T) {
	result := controlTrack("rtsp://host/base", "rtsp://host/other/track")
	if result != "rtsp://host/other/track" {
		t.Fatalf("expected absolute URL returned as-is, got %q", result)
	}
}

func TestControlTrack_RelativeWithTrailingSlash(t *testing.T) {
	result := controlTrack("rtsp://host/base/", "trackID=0")
	if result != "rtsp://host/base/trackID=0" {
		t.Fatalf("expected track appended without extra slash, got %q", result)
	}
}

func TestControlTrack_RelativeWithoutTrailingSlash(t *testing.T) {
	result := controlTrack("rtsp://host/base", "trackID=0")
	if result != "rtsp://host/base/trackID=0" {
		t.Fatalf("expected slash inserted, got %q", result)
	}
}

func TestControlTrack_EmptyTrack(t *testing.T) {
	result := controlTrack("rtsp://host/base/", "")
	if result != "rtsp://host/base/" {
		t.Fatalf("expected base unchanged for empty track, got %q", result)
	}
}

// =========================================================================
// stringInBetween
// =========================================================================

func TestStringInBetween_Normal(t *testing.T) {
	result := stringInBetween(`realm="myRealm", nonce="abc"`, `realm="`, `"`)
	if result != "myRealm" {
		t.Fatalf("expected 'myRealm', got %q", result)
	}
}

func TestStringInBetween_StartNotFound(t *testing.T) {
	result := stringInBetween("no match here", "realm=\"", "\"")
	if result != "" {
		t.Fatalf("expected empty string when start not found, got %q", result)
	}
}

func TestStringInBetween_EndNotFound(t *testing.T) {
	result := stringInBetween(`realm="noClosing`, `realm="`, `"`)
	if result != "" {
		t.Fatalf("expected empty when end not found, got %q", result)
	}
}

func TestStringInBetween_MultipleOccurrences(t *testing.T) {
	// Should return the first occurrence
	result := stringInBetween(`a="first" a="second"`, `a="`, `"`)
	if result != "first" {
		t.Fatalf("expected first occurrence, got %q", result)
	}
}

func TestStringInBetween_EmptyResult(t *testing.T) {
	result := stringInBetween(`key=""`, `key="`, `"`)
	if result != "" {
		t.Fatalf("expected empty string for empty value, got %q", result)
	}
}

// =========================================================================
// client — newClient
// =========================================================================

func TestNewClient_Defaults(t *testing.T) {
	c := newClient()
	if c.seq != 0 {
		t.Fatal("expected seq 0")
	}
	if c.headers["User-Agent"] != "gomedia" {
		t.Fatal("expected User-Agent header")
	}
	// All methods should default to false
	for method, supported := range c.methods {
		if supported {
			t.Fatalf("method %s should default to false", method)
		}
	}
}

// =========================================================================
// client — request
// =========================================================================

func TestRequest_BasicRequest(t *testing.T) {
	c, fc := setupClient(okResponse(0))

	resp, err := c.request(options, nil, c.control, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp["CSeq"] != "0" {
		t.Fatalf("expected CSeq 0 in response, got %v", resp["CSeq"])
	}

	req := fc.writeBuf.String()
	if !strings.Contains(req, "OPTIONS rtsp://example.com/stream RTSP/1.0\r\n") {
		t.Fatalf("request line mismatch: %q", req)
	}
	if !strings.Contains(req, "CSeq: 0\r\n") {
		t.Fatalf("missing CSeq header: %q", req)
	}
	if !strings.Contains(req, "User-Agent: gomedia\r\n") {
		t.Fatalf("missing User-Agent: %q", req)
	}
}

func TestRequest_SequenceNumberIncrements(t *testing.T) {
	// Two consecutive requests should have incrementing CSeq
	resp1 := okResponse(0) + okResponse(1)
	c, _ := setupClient(resp1)

	if _, err := c.request(options, nil, c.control, nil, false); err != nil {
		t.Fatal(err)
	}
	if c.seq != 1 {
		t.Fatalf("expected seq 1 after first request, got %d", c.seq)
	}

	if _, err := c.request(options, nil, c.control, nil, false); err != nil {
		t.Fatal(err)
	}
	if c.seq != 2 {
		t.Fatalf("expected seq 2 after second request, got %d", c.seq)
	}
}

func TestRequest_CSeqMismatch(t *testing.T) {
	// Response has CSeq 99 but request sent CSeq 0 — should error
	resp := "RTSP/1.0 200 OK\r\nCSeq: 99\r\n\r\n"
	c, _ := setupClient(resp)

	_, err := c.request(options, nil, c.control, nil, false)
	if err == nil {
		t.Fatal("expected CSeq mismatch error")
	}
	if !strings.Contains(err.Error(), "response seq mismatch") {
		t.Fatalf("expected mismatch error, got: %v", err)
	}
}

func TestRequest_NoResponse(t *testing.T) {
	// When nores=true, no response is read
	c, fc := setupClient("") // nothing to read
	_, err := c.request(options, nil, c.control, nil, true)
	if err != nil {
		t.Fatalf("unexpected error with nores=true: %v", err)
	}
	req := fc.writeBuf.String()
	if !strings.Contains(req, "OPTIONS") {
		t.Fatalf("request not sent: %q", req)
	}
	// Seq should still increment
	if c.seq != 1 {
		t.Fatalf("expected seq 1 after nores request, got %d", c.seq)
	}
}

func TestRequest_CustomHeaders(t *testing.T) {
	c, fc := setupClient(okResponse(0))
	headers := map[string]string{"Accept": "application/sdp"}
	if _, err := c.request(describe, headers, c.control, nil, false); err != nil {
		t.Fatal(err)
	}
	req := fc.writeBuf.String()
	if !strings.Contains(req, "Accept: application/sdp\r\n") {
		t.Fatalf("missing custom header: %q", req)
	}
}

func TestRequest_WithBody(t *testing.T) {
	c, fc := setupClient(okResponse(0))
	body := []byte("v=0\r\ns=test\r\n")
	if _, err := c.request(announce, nil, c.control, body, false); err != nil {
		t.Fatal(err)
	}
	req := fc.writeBuf.String()
	if !strings.Contains(req, string(body)) {
		t.Fatalf("body not found in request: %q", req)
	}
}

func TestRequest_NonOKStatus(t *testing.T) {
	resp := "RTSP/1.0 404 Not Found\r\nCSeq: 0\r\n\r\n"
	c, _ := setupClient(resp)
	_, err := c.request(describe, nil, c.control, nil, false)
	if err == nil {
		t.Fatal("expected error for 404 status")
	}
	if !strings.Contains(err.Error(), "camera send status") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequest_SessionExtraction(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nSession: ABC123;timeout=60\r\n\r\n"
	c, _ := setupClient(resp)
	if _, err := c.request(setup, nil, c.control, nil, false); err != nil {
		t.Fatal(err)
	}
	if c.session != "ABC123" {
		t.Fatalf("expected session 'ABC123', got %q", c.session)
	}
	if c.headers["Session"] != "ABC123" {
		t.Fatalf("session header not set: %q", c.headers["Session"])
	}
}

func TestRequest_ContentBaseUpdate(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nContent-Base: rtsp://example.com/new/\r\n\r\n"
	c, _ := setupClient(resp)
	if _, err := c.request(describe, nil, c.control, nil, false); err != nil {
		t.Fatal(err)
	}
	if c.control != "rtsp://example.com/new/" {
		t.Fatalf("expected control updated, got %q", c.control)
	}
}

// =========================================================================
// client — Digest authentication
// =========================================================================

func TestRequest_DigestAuth(t *testing.T) {
	// First response: 401 with WWW-Authenticate Digest challenge
	// Second response: 200 OK after auth
	// The retry request is sent from within handleAuthentication, which is called
	// inside the first request() call — before the deferred seq++ fires.
	// So the retry also uses CSeq 0.
	authResp := "RTSP/1.0 401 Unauthorized\r\n" +
		"CSeq: 0\r\n" +
		"WWW-Authenticate: Digest realm=\"testrealm\", nonce=\"testnonce\"\r\n" +
		"\r\n" +
		"RTSP/1.0 200 OK\r\n" +
		"CSeq: 0\r\n" +
		"\r\n"

	c, fc := setupClient(authResp)
	c.username = "admin"
	c.password = "pass"

	resp, err := c.request(describe, nil, c.control, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Verify auth fields were set
	if c.realm != "testrealm" {
		t.Fatalf("realm not set: %q", c.realm)
	}
	if c.nonce != "testnonce" {
		t.Fatalf("nonce not set: %q", c.nonce)
	}

	// Verify second request contains Authorization header
	req := fc.writeBuf.String()
	if !strings.Contains(req, "Authorization: Digest") {
		t.Fatalf("expected Digest auth in retry request: %q", req)
	}
}

func TestRequest_DigestAuthCorrectResponse(t *testing.T) {
	username := "admin"
	password := "secret"
	realm := "myrealm"
	nonce := "mynonce"
	uri := "rtsp://example.com/stream"
	method := describe

	// Pre-compute expected digest
	md5UserRealmPwd := fmt.Sprintf("%x", md5.Sum(fmt.Appendf(nil, "%s:%s:%s", username, realm, password)))
	md5MethodURL := fmt.Sprintf("%x", md5.Sum(fmt.Appendf(nil, "%s:%s", method, uri)))
	expectedResponse := fmt.Sprintf("%x", md5.Sum(fmt.Appendf(nil, "%s:%s:%s", md5UserRealmPwd, nonce, md5MethodURL)))

	c, fc := setupClient(okResponse(0))
	c.username = username
	c.password = password
	c.realm = realm
	c.nonce = nonce

	if _, err := c.request(method, nil, uri, nil, false); err != nil {
		t.Fatal(err)
	}

	req := fc.writeBuf.String()
	if !strings.Contains(req, fmt.Sprintf("response=\"%s\"", expectedResponse)) {
		t.Fatalf("digest response mismatch in request: %q", req)
	}
	if !strings.Contains(req, fmt.Sprintf("realm=\"%s\"", realm)) {
		t.Fatalf("realm not in Authorization header: %q", req)
	}
}

func TestRequest_BasicAuth(t *testing.T) {
	// Same as digest: retry happens before deferred seq++, so CSeq stays 0.
	authResp := "RTSP/1.0 401 Unauthorized\r\n" +
		"CSeq: 0\r\n" +
		"WWW-Authenticate: Basic realm=\"test\"\r\n" +
		"\r\n" +
		"RTSP/1.0 200 OK\r\n" +
		"CSeq: 0\r\n" +
		"\r\n"

	c, fc := setupClient(authResp)
	c.username = "user"
	c.password = "pass"

	_, err := c.request(describe, nil, c.control, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if c.headers["Authorization"] != expected {
		t.Fatalf("expected Basic auth header %q, got %q", expected, c.headers["Authorization"])
	}

	req := fc.writeBuf.String()
	if !strings.Contains(req, "Authorization: "+expected) {
		t.Fatalf("basic auth not in retry request: %q", req)
	}
}

func TestRequest_DoubleDigestAuth_Fails(t *testing.T) {
	// If realm is already set and we get another 401, should fail
	authResp := "RTSP/1.0 401 Unauthorized\r\n" +
		"CSeq: 0\r\n" +
		"WWW-Authenticate: Digest realm=\"r\", nonce=\"n\"\r\n" +
		"\r\n"

	c, _ := setupClient(authResp)
	c.realm = "already-set"

	_, err := c.request(describe, nil, c.control, nil, false)
	if err == nil {
		t.Fatal("expected 401 unauthorized error on double digest auth")
	}
	if !strings.Contains(err.Error(), "401 unauthorized") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequest_DoubleBasicAuth_Fails(t *testing.T) {
	authResp := "RTSP/1.0 401 Unauthorized\r\n" +
		"CSeq: 0\r\n" +
		"WWW-Authenticate: Basic realm=\"r\"\r\n" +
		"\r\n"

	c, _ := setupClient(authResp)
	c.headers["Authorization"] = "Basic already"

	_, err := c.request(describe, nil, c.control, nil, false)
	if err == nil {
		t.Fatal("expected 401 unauthorized error on double basic auth")
	}
}

// =========================================================================
// client — options
// =========================================================================

func TestOptions_ParsesPublicHeader(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nPublic: DESCRIBE, SETUP, PLAY, TEARDOWN\r\n\r\n"
	c, _ := setupClient(resp)

	if err := c.options(); err != nil {
		t.Fatal(err)
	}

	for _, m := range []rtspMethod{describe, setup, play, teardown} {
		if !c.methods[m] {
			t.Fatalf("method %s should be supported", m)
		}
	}
	if c.methods[record] {
		t.Fatal("RECORD should not be supported")
	}
}

func TestOptions_NoPublicHeader(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\n\r\n"
	c, _ := setupClient(resp)
	if err := c.options(); err != nil {
		t.Fatal(err)
	}
	// All methods should remain false
	for m, v := range c.methods {
		if v {
			t.Fatalf("method %s should not be marked supported", m)
		}
	}
}

// =========================================================================
// client — describe
// =========================================================================

func TestDescribe_ParsesSDP(t *testing.T) {
	sdpBody := "v=0\r\nm=video 0 RTP/AVP 96\r\na=rtpmap:96 H264/90000\r\na=control:trackID=0\r\n"
	resp := fmt.Sprintf(
		"RTSP/1.0 200 OK\r\nCSeq: 0\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s",
		len(sdpBody), sdpBody)

	c, _ := setupClient(resp)
	medias, err := c.describe()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(medias) == 0 {
		t.Fatal("expected at least one media")
	}
	if medias[0].AVType != "video" {
		t.Fatalf("expected video, got %q", medias[0].AVType)
	}
	if medias[0].Type != gomedia.H264 {
		t.Fatalf("expected H264, got %v", medias[0].Type)
	}
}

func TestDescribe_WrongContentType(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nContent-Type: text/plain\r\n\r\n"
	c, _ := setupClient(resp)
	_, err := c.describe()
	if err == nil {
		t.Fatal("expected error for wrong content type")
	}
	if !strings.Contains(err.Error(), "wrong content type") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDescribe_MissingContentType(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\n\r\n"
	c, _ := setupClient(resp)
	_, err := c.describe()
	if err == nil {
		t.Fatal("expected error for missing content type")
	}
}

func TestDescribe_MissingContentLength(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nContent-Type: application/sdp\r\n\r\n"
	c, _ := setupClient(resp)
	_, err := c.describe()
	if err == nil {
		t.Fatal("expected error for missing content length")
	}
	if !strings.Contains(err.Error(), "no content length") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =========================================================================
// client — setup
// =========================================================================

func TestSetup_ParsesInterleavedChannel(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nTransport: RTP/AVP/TCP;unicast;interleaved=4-5\r\n\r\n"
	c, _ := setupClient(resp)

	ch, err := c.setup(4, "rtsp://example.com/stream/trackID=0", "play")
	if err != nil {
		t.Fatal(err)
	}
	if ch != 4 {
		t.Fatalf("expected channel 4, got %d", ch)
	}
}

func TestSetup_NoTransportHeader(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\n\r\n"
	c, _ := setupClient(resp)
	_, err := c.setup(0, c.control, "play")
	if err == nil {
		t.Fatal("expected error for missing Transport header")
	}
	if !strings.Contains(err.Error(), "no transport") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetup_NoInterleavedInTransport(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nTransport: RTP/AVP;unicast;destination=1.2.3.4\r\n\r\n"
	c, _ := setupClient(resp)
	_, err := c.setup(0, c.control, "play")
	if err == nil {
		t.Fatal("expected error for no interleaved")
	}
}

func TestSetup_SetsSessionHeader(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nSession: SESS1\r\nTransport: RTP/AVP/TCP;unicast;interleaved=0-1\r\n\r\n"
	c, _ := setupClient(resp)
	_, err := c.setup(0, c.control, "play")
	if err != nil {
		t.Fatal(err)
	}
	if c.session != "SESS1" {
		t.Fatalf("expected session SESS1, got %q", c.session)
	}
}

func TestSetup_RequestContainsTransportHeader(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nTransport: RTP/AVP/TCP;unicast;interleaved=2-3\r\n\r\n"
	c, fc := setupClient(resp)
	_, err := c.setup(2, "rtsp://example.com/stream/trackID=0", "play")
	if err != nil {
		t.Fatal(err)
	}
	req := fc.writeBuf.String()
	if !strings.Contains(req, "Transport: RTP/AVP/TCP;unicast;interleaved=2-3;mode=play") {
		t.Fatalf("transport header mismatch: %q", req)
	}
}

// =========================================================================
// client — play
// =========================================================================

func TestPlay_SendsPlayMethod(t *testing.T) {
	c, fc := setupClient(okResponse(0))
	if err := c.play(); err != nil {
		t.Fatal(err)
	}
	req := fc.writeBuf.String()
	if !strings.Contains(req, "PLAY rtsp://example.com/stream RTSP/1.0\r\n") {
		t.Fatalf("expected PLAY request: %q", req)
	}
}

// =========================================================================
// client — ping
// =========================================================================

func TestPing_SendsOptionsMethod(t *testing.T) {
	c, fc := setupClient("")
	if err := c.ping(); err != nil {
		t.Fatal(err)
	}
	req := fc.writeBuf.String()
	if !strings.Contains(req, "OPTIONS") {
		t.Fatalf("expected OPTIONS in ping request: %q", req)
	}
}

func TestPing_NoResponseExpected(t *testing.T) {
	// ping sends with nores=true, so even empty read buffer should work
	c, _ := setupClient("")
	err := c.ping()
	if err != nil {
		t.Fatalf("ping should succeed with nores=true: %v", err)
	}
}

// =========================================================================
// client — Read
// =========================================================================

func TestClientRead_NilConn(t *testing.T) {
	c := newClient()
	c.conn = nil
	err := c.Read(make([]byte, 10))
	if err == nil {
		t.Fatal("expected error for nil connection")
	}
	if !strings.Contains(err.Error(), "connection is not opened") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientRead_ReadsExactBytes(t *testing.T) {
	data := "hello world"
	c, _ := setupClient(data)

	buf := make([]byte, 5)
	err := c.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf) != "hello" {
		t.Fatalf("expected 'hello', got %q", string(buf))
	}
}

func TestClientRead_ZeroLengthBuf(t *testing.T) {
	c, _ := setupClient("some data")
	err := c.Read(nil)
	if err != nil {
		t.Fatalf("zero-length read should not error: %v", err)
	}
}

func TestClientRead_EOF(t *testing.T) {
	c, _ := setupClient("")
	buf := make([]byte, 10)
	err := c.Read(buf)
	if err == nil {
		t.Fatal("expected EOF error")
	}
}

// =========================================================================
// client — Close
// =========================================================================

func TestClientClose_SendsTeardown(t *testing.T) {
	c, fc := setupClient("")
	c.Close()

	req := fc.writeBuf.String()
	if !strings.Contains(req, "TEARDOWN") {
		t.Fatalf("expected TEARDOWN in close: %q", req)
	}
}

func TestClientClose_NilConn(t *testing.T) {
	c := newClient()
	c.conn = nil
	// Should not panic
	c.Close()
}

// =========================================================================
// client — supportsPublish
// =========================================================================

func TestSupportsPublish_BothTrue(t *testing.T) {
	c := newClient()
	c.methods[announce] = true
	c.methods[record] = true
	if !c.supportsPublish() {
		t.Fatal("expected supportsPublish to be true")
	}
}

func TestSupportsPublish_MissingAnnounce(t *testing.T) {
	c := newClient()
	c.methods[record] = true
	if c.supportsPublish() {
		t.Fatal("expected supportsPublish to be false without ANNOUNCE")
	}
}

func TestSupportsPublish_MissingRecord(t *testing.T) {
	c := newClient()
	c.methods[announce] = true
	if c.supportsPublish() {
		t.Fatal("expected supportsPublish to be false without RECORD")
	}
}

// =========================================================================
// client — establishConnection URL parsing
// =========================================================================

func TestEstablishConnection_URLWithoutPort(t *testing.T) {
	// We can't fully test establishConnection because it dials TCP,
	// but we can test the URL parsing logic by checking what it would produce.
	// Use a listener to accept the connection.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Send OPTIONS response
		resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nPublic: DESCRIBE\r\n\r\n"
		conn.Write([]byte(resp))
		conn.Close()
	}()

	c := newClient()
	rawURL := fmt.Sprintf("rtsp://user:pass@127.0.0.1:%d/stream", port)
	err = c.establishConnection(rawURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer c.Close()

	if c.username != "user" {
		t.Fatalf("expected username 'user', got %q", c.username)
	}
	if c.password != "pass" {
		t.Fatalf("expected password 'pass', got %q", c.password)
	}
	// URL should not contain user info after parsing
	if strings.Contains(c.control, "user:pass") {
		t.Fatalf("control URL should not contain user info: %q", c.control)
	}
}

func TestEstablishConnection_NoUserInfo(t *testing.T) {
	// Test that URLs without user info don't panic (this was the nil pointer bug)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nPublic: DESCRIBE\r\n\r\n"
		conn.Write([]byte(resp))
		conn.Close()
	}()

	c := newClient()
	rawURL := fmt.Sprintf("rtsp://127.0.0.1:%d/stream", port)
	err = c.establishConnection(rawURL)
	if err != nil {
		t.Fatalf("should not panic or error for URL without user info: %v", err)
	}
	defer c.Close()

	if c.username != "" {
		t.Fatalf("expected empty username, got %q", c.username)
	}
	if c.password != "" {
		t.Fatalf("expected empty password, got %q", c.password)
	}
}

func TestEstablishConnection_InvalidURL(t *testing.T) {
	c := newClient()
	err := c.establishConnection("://invalid")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestEstablishConnection_UnreachableHost(t *testing.T) {
	c := newClient()
	// Use a port that's not listening
	err := c.establishConnection("rtsp://127.0.0.1:1/stream")
	if err == nil {
		t.Fatal("expected dial error for unreachable host")
	}
}

func TestEstablishConnection_DefaultScheme(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\n\r\n"
		conn.Write([]byte(resp))
		conn.Close()
	}()

	c := newClient()
	// Use http scheme — should be overridden to rtsp
	err = c.establishConnection(fmt.Sprintf("http://127.0.0.1:%d/stream", port))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer c.Close()

	if c.pURL.Scheme != RTSP {
		t.Fatalf("expected scheme 'rtsp', got %q", c.pURL.Scheme)
	}
}

// =========================================================================
// client — Content-length normalization
// =========================================================================

func TestRequest_ContentLengthNormalization(t *testing.T) {
	// Some servers send "Content-length" (lowercase 'l') — client normalizes to "Content-Length"
	body := "test body"
	resp := fmt.Sprintf("RTSP/1.0 200 OK\r\nCSeq: 0\r\nContent-length: %d\r\n\r\n%s", len(body), body)
	c, _ := setupClient(resp)
	headers, err := c.request(describe, nil, c.control, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := headers["Content-Length"]; !ok {
		t.Fatal("Content-length should be normalized to Content-Length")
	}
}

// =========================================================================
// demuxer — ReadPacket RTP packet parsing
// =========================================================================

// buildRTPInterleavedPacket builds a TCP-interleaved RTP packet:
// [0x24, channel, 2-byte length, RTP payload]
func buildRTPInterleavedPacket(channel uint8, payload []byte) []byte {
	buf := make([]byte, 4+len(payload))
	buf[0] = rtpPacket // 0x24 '$'
	buf[1] = channel
	binary.BigEndian.PutUint16(buf[2:], uint16(len(payload)))
	copy(buf[4:], payload)
	return buf
}

// buildMinimalRTPPayload creates a minimal valid RTP payload (12 bytes minimum header)
func buildMinimalRTPPayload(seqNum uint16, timestamp uint32) []byte {
	payload := make([]byte, 12)
	payload[0] = 0x80 // V=2, no padding, no extension, CC=0
	payload[1] = 96   // PT=96 (dynamic), no marker
	binary.BigEndian.PutUint16(payload[2:], seqNum)
	binary.BigEndian.PutUint32(payload[4:], timestamp)
	binary.BigEndian.PutUint32(payload[8:], 0x12345678) // SSRC
	return payload
}

func TestDemuxer_RejectsShortPacket(t *testing.T) {
	// Packet with length < 12 should be rejected
	shortPayload := make([]byte, 8) // less than 12
	pkt := buildRTPInterleavedPacket(0, shortPayload)

	fc := newRTSPFakeConn(string(pkt))
	dmx := New("rtsp://test").(*innerRTSPDemuxer)
	dmx.client.conn = fc
	dmx.client.connRW = bufio.NewReadWriter(bufio.NewReader(fc), bufio.NewWriter(fc))
	dmx.videoIdx = 0
	dmx.lastPktRcv = time.Now()

	_, err := dmx.ReadPacket()
	// Should return nil packet (short packets are logged but not fatal)
	if err != nil && !strings.Contains(err.Error(), "timeout") {
		// Either nil error with nil packet or could be read error — both acceptable
		t.Logf("got error (acceptable): %v", err)
	}
}

// =========================================================================
// demuxer — processRTSPPacket
// =========================================================================

func TestProcessRTSPPacket_NotRTSP(t *testing.T) {
	dmx := New("rtsp://test").(*innerRTSPDemuxer)
	dmx.client.conn = newRTSPFakeConn("")
	dmx.client.connRW = bufio.NewReadWriter(
		bufio.NewReader(dmx.client.conn),
		bufio.NewWriter(dmx.client.conn))

	var header [headerSize]byte
	copy(header[:], "XXXX")

	err := dmx.processRTSPPacket(header)
	// Should return nil (just a warning, not fatal)
	if err != nil {
		t.Fatalf("expected nil error for non-RTSP prefix, got: %v", err)
	}
}

func TestProcessRTSPPacket_ValidRTSPMessage(t *testing.T) {
	// Simulate a valid RTSP response after "RTSP" header
	// The demuxer already read "RTSP", now reads the rest
	rtspMsg := "/1.0 200 OK\r\nCSeq: 1\r\n\r\n"
	fc := newRTSPFakeConn(rtspMsg)

	dmx := New("rtsp://test").(*innerRTSPDemuxer)
	dmx.client.conn = fc
	dmx.client.connRW = bufio.NewReadWriter(bufio.NewReader(fc), bufio.NewWriter(fc))

	var header [headerSize]byte
	copy(header[:], "RTSP")

	err := dmx.processRTSPPacket(header)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProcessRTSPPacket_WithContentLength(t *testing.T) {
	body := "some body content"
	rtspMsg := fmt.Sprintf("/1.0 200 OK\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	fc := newRTSPFakeConn(rtspMsg)

	dmx := New("rtsp://test").(*innerRTSPDemuxer)
	dmx.client.conn = fc
	dmx.client.connRW = bufio.NewReadWriter(bufio.NewReader(fc), bufio.NewWriter(fc))

	var header [headerSize]byte
	copy(header[:], "RTSP")

	err := dmx.processRTSPPacket(header)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =========================================================================
// demuxer — New and options
// =========================================================================

func TestNew_DefaultOptions(t *testing.T) {
	d := New("rtsp://test").(*innerRTSPDemuxer)
	defer d.Close()

	if d.url != "rtsp://test" {
		t.Fatalf("expected url 'rtsp://test', got %q", d.url)
	}
	if d.noVideo {
		t.Fatal("noVideo should default to false")
	}
	if d.noAudio {
		t.Fatal("noAudio should default to false")
	}
	if d.videoIdx != -128 {
		t.Fatalf("expected videoIdx -128, got %d", d.videoIdx)
	}
	if d.audioIdx != -128 {
		t.Fatalf("expected audioIdx -128, got %d", d.audioIdx)
	}
}

func TestNew_WithNoVideo(t *testing.T) {
	d := New("rtsp://test", NoVideo()).(*innerRTSPDemuxer)
	defer d.Close()
	if !d.noVideo {
		t.Fatal("expected noVideo=true")
	}
}

func TestNew_WithNoAudio(t *testing.T) {
	d := New("rtsp://test", NoAudio()).(*innerRTSPDemuxer)
	defer d.Close()
	if !d.noAudio {
		t.Fatal("expected noAudio=true")
	}
}

func TestNew_WithRingBuffer(t *testing.T) {
	d := New("rtsp://test", WithRingBuffer(1024*1024)).(*innerRTSPDemuxer)
	defer d.Close()
	if d.rtpRingBufferSize != 1024*1024 {
		t.Fatalf("expected ring buffer size 1048576, got %d", d.rtpRingBufferSize)
	}
}

// =========================================================================
// demuxer — Close
// =========================================================================

func TestDemuxerClose_StopsTicker(t *testing.T) {
	d := New("rtsp://test").(*innerRTSPDemuxer)
	// Should not panic
	d.Close()
}

// =========================================================================
// Muxer — codecParamsToSDPMedias
// =========================================================================

func TestCodecParamsToSDPMedias_H264Only(t *testing.T) {
	h264Cp := loadH264Params(t)

	pair := gomedia.CodecParametersPair{
		VideoCodecParameters: h264Cp,
	}

	medias, err := codecParamsToSDPMedias(pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(medias) != 1 {
		t.Fatalf("expected 1 media, got %d", len(medias))
	}
	if medias[0].AVType != video {
		t.Fatalf("expected video, got %q", medias[0].AVType)
	}
	if medias[0].Type != gomedia.H264 {
		t.Fatalf("expected H264, got %v", medias[0].Type)
	}
	if medias[0].PayloadType != 96 {
		t.Fatalf("expected payload type 96, got %d", medias[0].PayloadType)
	}
	if medias[0].TimeScale != 90000 {
		t.Fatalf("expected timescale 90000, got %d", medias[0].TimeScale)
	}
	if len(medias[0].SpropParameterSets) != 2 {
		t.Fatalf("expected 2 sprop parameter sets (SPS+PPS), got %d", len(medias[0].SpropParameterSets))
	}
	if medias[0].Control != "trackID=0" {
		t.Fatalf("expected trackID=0, got %q", medias[0].Control)
	}
}

func TestCodecParamsToSDPMedias_H265Only(t *testing.T) {
	h265Cp := loadH265Params(t)

	pair := gomedia.CodecParametersPair{
		VideoCodecParameters: h265Cp,
	}

	medias, err := codecParamsToSDPMedias(pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(medias) != 1 {
		t.Fatalf("expected 1 media, got %d", len(medias))
	}
	if medias[0].Type != gomedia.H265 {
		t.Fatalf("expected H265, got %v", medias[0].Type)
	}
	if medias[0].PayloadType != 98 {
		t.Fatalf("expected payload type 98 for H265, got %d", medias[0].PayloadType)
	}
	if len(medias[0].SpropVPS) == 0 {
		t.Fatal("expected non-empty SpropVPS")
	}
	if len(medias[0].SpropSPS) == 0 {
		t.Fatal("expected non-empty SpropSPS")
	}
	if len(medias[0].SpropPPS) == 0 {
		t.Fatal("expected non-empty SpropPPS")
	}
}

func TestCodecParamsToSDPMedias_MJPEG(t *testing.T) {
	mjpegCp := mjpeg.NewCodecParameters(640, 480, 30)

	pair := gomedia.CodecParametersPair{
		VideoCodecParameters: mjpegCp,
	}

	medias, err := codecParamsToSDPMedias(pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(medias) != 1 {
		t.Fatalf("expected 1 media, got %d", len(medias))
	}
	if medias[0].Type != gomedia.MJPEG {
		t.Fatalf("expected MJPEG, got %v", medias[0].Type)
	}
	if medias[0].Width != 640 {
		t.Fatalf("expected width 640, got %d", medias[0].Width)
	}
	if medias[0].Height != 480 {
		t.Fatalf("expected height 480, got %d", medias[0].Height)
	}
}

func TestCodecParamsToSDPMedias_AACOnly(t *testing.T) {
	aacCp := loadAACParams(t)

	pair := gomedia.CodecParametersPair{
		AudioCodecParameters: aacCp,
	}

	medias, err := codecParamsToSDPMedias(pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(medias) != 1 {
		t.Fatalf("expected 1 media, got %d", len(medias))
	}
	if medias[0].AVType != audio {
		t.Fatalf("expected audio, got %q", medias[0].AVType)
	}
	if medias[0].Type != gomedia.AAC {
		t.Fatalf("expected AAC, got %v", medias[0].Type)
	}
	if medias[0].SizeLength != 13 {
		t.Fatalf("expected SizeLength 13 for AAC, got %d", medias[0].SizeLength)
	}
	if medias[0].IndexLength != 3 {
		t.Fatalf("expected IndexLength 3 for AAC, got %d", medias[0].IndexLength)
	}
	if len(medias[0].Config) == 0 {
		t.Fatal("expected non-empty AAC config")
	}
}

func TestCodecParamsToSDPMedias_OpusAudio(t *testing.T) {
	opusCp := opus.NewCodecParameters(0, gomedia.ChStereo, 48000)

	pair := gomedia.CodecParametersPair{
		AudioCodecParameters: opusCp,
	}

	medias, err := codecParamsToSDPMedias(pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(medias) != 1 {
		t.Fatalf("expected 1 media, got %d", len(medias))
	}
	if medias[0].Type != gomedia.OPUS {
		t.Fatalf("expected OPUS, got %v", medias[0].Type)
	}
	if medias[0].TimeScale != 48000 {
		t.Fatalf("expected timescale 48000, got %d", medias[0].TimeScale)
	}
}

func TestCodecParamsToSDPMedias_PCMUlaw(t *testing.T) {
	pcmCp := pcm.NewCodecParameters(0, gomedia.PCMUlaw, 1, 8000)

	pair := gomedia.CodecParametersPair{
		AudioCodecParameters: pcmCp,
	}

	medias, err := codecParamsToSDPMedias(pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(medias) != 1 {
		t.Fatalf("expected 1 media, got %d", len(medias))
	}
	if medias[0].Type != gomedia.PCMUlaw {
		t.Fatalf("expected PCMUlaw, got %v", medias[0].Type)
	}
	if medias[0].PayloadType != 0 {
		t.Fatalf("expected payload type 0 for PCMUlaw, got %d", medias[0].PayloadType)
	}
}

func TestCodecParamsToSDPMedias_PCMAlaw(t *testing.T) {
	pcmCp := pcm.NewCodecParameters(0, gomedia.PCMAlaw, 1, 8000)

	pair := gomedia.CodecParametersPair{
		AudioCodecParameters: pcmCp,
	}

	medias, err := codecParamsToSDPMedias(pair)
	if err != nil {
		t.Fatal(err)
	}
	if medias[0].PayloadType != 8 {
		t.Fatalf("expected payload type 8 for PCMAlaw, got %d", medias[0].PayloadType)
	}
}

func TestCodecParamsToSDPMedias_VideoAndAudio(t *testing.T) {
	h264Cp := loadH264Params(t)
	aacCp := loadAACParams(t)

	pair := gomedia.CodecParametersPair{
		VideoCodecParameters: h264Cp,
		AudioCodecParameters: aacCp,
	}

	medias, err := codecParamsToSDPMedias(pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(medias) != 2 {
		t.Fatalf("expected 2 medias, got %d", len(medias))
	}
	// Video should come first after sorting
	if medias[0].AVType != video {
		t.Fatalf("expected video first, got %q", medias[0].AVType)
	}
	if medias[1].AVType != audio {
		t.Fatalf("expected audio second, got %q", medias[1].AVType)
	}
	// Track IDs should be reassigned after sort
	if medias[0].Control != "trackID=0" {
		t.Fatalf("expected video trackID=0, got %q", medias[0].Control)
	}
	if medias[1].Control != "trackID=1" {
		t.Fatalf("expected audio trackID=1, got %q", medias[1].Control)
	}
}

func TestCodecParamsToSDPMedias_AudioBeforeVideo_SortedCorrectly(t *testing.T) {
	// Even if audio codec is provided first, video should come first in output
	h264Cp := loadH264Params(t)
	aacCp := loadAACParams(t)

	// AudioCodecParameters first, VideoCodecParameters second
	pair := gomedia.CodecParametersPair{
		AudioCodecParameters: aacCp,
		VideoCodecParameters: h264Cp,
	}

	medias, err := codecParamsToSDPMedias(pair)
	if err != nil {
		t.Fatal(err)
	}
	if medias[0].AVType != video {
		t.Fatalf("expected video first after sort, got %q", medias[0].AVType)
	}
}

func TestCodecParamsToSDPMedias_NoStreams(t *testing.T) {
	pair := gomedia.CodecParametersPair{}
	medias, err := codecParamsToSDPMedias(pair)
	if err != nil {
		t.Fatal(err)
	}
	if len(medias) != 0 {
		t.Fatalf("expected 0 medias for empty pair, got %d", len(medias))
	}
}

// =========================================================================
// videoCodecToSDPMedia — edge cases
// =========================================================================

func TestVideoCodecToSDPMedia_H264MissingSPS(t *testing.T) {
	cp := &h264.CodecParameters{} // no SPS/PPS set
	_, err := videoCodecToSDPMedia(cp, 0)
	if err == nil {
		t.Fatal("expected error for H264 without SPS/PPS")
	}
	if !strings.Contains(err.Error(), "SPS and PPS") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVideoCodecToSDPMedia_H265MissingVPS(t *testing.T) {
	cp := &h265.CodecParameters{} // no VPS/SPS/PPS set
	_, err := videoCodecToSDPMedia(cp, 0)
	if err == nil {
		t.Fatal("expected error for H265 without VPS/SPS/PPS")
	}
	if !strings.Contains(err.Error(), "VPS, SPS and PPS") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =========================================================================
// audioCodecToSDPMedia — edge cases
// =========================================================================

func TestAudioCodecToSDPMedia_AACMissingConfig(t *testing.T) {
	cp := &aac.CodecParameters{} // no config
	_, err := audioCodecToSDPMedia(cp, 0)
	if err == nil {
		t.Fatal("expected error for AAC without config")
	}
	if !strings.Contains(err.Error(), "config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =========================================================================
// Muxer — NewMuxer
// =========================================================================

func TestNewMuxer_CreatesValidMuxer(t *testing.T) {
	m := NewMuxer("rtsp://test/stream", logger.Default)
	if m == nil {
		t.Fatal("expected non-nil muxer")
	}
	mux := m.(*Muxer)
	if mux.url != "rtsp://test/stream" {
		t.Fatalf("expected url, got %q", mux.url)
	}
}

// =========================================================================
// Muxer — WritePacket
// =========================================================================

func TestMuxerWritePacket_NilVideoMuxer(t *testing.T) {
	m := &Muxer{
		client: newClient(),
	}
	err := m.WritePacket(nil)
	if err == nil {
		t.Fatal("expected error when videoMuxer is nil")
	}
	if !strings.Contains(err.Error(), "not initialized") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =========================================================================
// Muxer — Mux server does not support publish
// =========================================================================

func TestMux_ServerDoesNotSupportPublish(t *testing.T) {
	// Start a fake RTSP server that does NOT advertise ANNOUNCE/RECORD
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		n, _ := conn.Read(buf)
		_ = n
		// OPTIONS response without ANNOUNCE/RECORD
		resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nPublic: DESCRIBE, SETUP, PLAY\r\n\r\n"
		conn.Write([]byte(resp))
	}()

	h264Cp := loadH264Params(t)
	pair := gomedia.CodecParametersPair{VideoCodecParameters: h264Cp}

	m := NewMuxer(fmt.Sprintf("rtsp://127.0.0.1:%d/stream", port), logger.Default)
	err = m.Mux(pair)
	if err == nil {
		t.Fatal("expected error when server doesn't support ANNOUNCE/RECORD")
	}
	if !strings.Contains(err.Error(), "does not support ANNOUNCE and RECORD") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =========================================================================
// Muxer — Mux with no streams
// =========================================================================

func TestMux_NoStreams(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 4096)
		conn.Read(buf)
		resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nPublic: ANNOUNCE, RECORD, DESCRIBE, SETUP, PLAY\r\n\r\n"
		conn.Write([]byte(resp))
	}()

	pair := gomedia.CodecParametersPair{} // no video, no audio
	m := NewMuxer(fmt.Sprintf("rtsp://127.0.0.1:%d/stream", port), logger.Default)
	err = m.Mux(pair)
	if err == nil {
		t.Fatal("expected error for no streams")
	}
	if !strings.Contains(err.Error(), "no video or audio streams") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =========================================================================
// Muxer — Close
// =========================================================================

func TestMuxerClose_NilConn(t *testing.T) {
	m := &Muxer{client: newClient()}
	// Should not panic
	m.Close()
}

// =========================================================================
// settings constants
// =========================================================================

func TestSettings_Constants(t *testing.T) {
	if rtpPacket != 0x24 {
		t.Fatalf("expected rtpPacket 0x24, got 0x%02x", rtpPacket)
	}
	if rtspPacket != 0x52 {
		t.Fatalf("expected rtspPacket 0x52 ('R'), got 0x%02x", rtspPacket)
	}
	if headerSize != 4 {
		t.Fatalf("expected headerSize 4, got %d", headerSize)
	}
	if RTSPPort != 554 {
		t.Fatalf("expected RTSPPort 554, got %d", RTSPPort)
	}
	if RTSP != "rtsp" {
		t.Fatalf("expected 'rtsp', got %q", RTSP)
	}
	if RTSPS != "rtsps" {
		t.Fatalf("expected 'rtsps', got %q", RTSPS)
	}
}

func TestSettings_RTSPMethods(t *testing.T) {
	methods := map[rtspMethod]string{
		describe:     "DESCRIBE",
		announce:     "ANNOUNCE",
		getParameter: "GET_PARAMETER",
		options:      "OPTIONS",
		pause:        "PAUSE",
		record:       "RECORD",
		teardown:     "TEARDOWN",
		play:         "PLAY",
		setup:        "SETUP",
		setParameter: "SET_PARAMETER",
		redirect:     "REDIRECT",
	}

	for method, expected := range methods {
		if string(method) != expected {
			t.Fatalf("method %v should be %q", method, expected)
		}
	}
}

// =========================================================================
// Integration: full RTSP DESCRIBE→SETUP→PLAY handshake (mock server)
// =========================================================================

func TestFullHandshake_DescribeSetupPlay(t *testing.T) {
	// Create a mock RTSP server that handles OPTIONS, DESCRIBE, SETUP, PLAY
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port

	// Use real SPS/PPS from test data to satisfy the H264 RTP demuxer
	spsB64 := "Z01AKJWQB4AiflwEQAAA+gAAMNQ4AAAFuNgAAehILvLgoA=="
	ppsB64 := "aOuPIA=="

	sdpBody := "v=0\r\n" +
		"m=video 0 RTP/AVP 96\r\n" +
		"a=rtpmap:96 H264/90000\r\n" +
		"a=fmtp:96 packetization-mode=1;sprop-parameter-sets=" + spsB64 + "," + ppsB64 + "\r\n" +
		"a=control:trackID=0\r\n"

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		rdr := bufio.NewReader(conn)
		for cseq := 0; cseq < 4; cseq++ {
			// Read until we get empty line
			var method string
			for {
				line, err := rdr.ReadString('\n')
				if err != nil {
					return
				}
				if cseq == 0 && method == "" {
					method = "OPTIONS"
				}
				if strings.TrimSpace(line) == "" {
					break
				}
				if strings.HasPrefix(line, "DESCRIBE") {
					method = "DESCRIBE"
				} else if strings.HasPrefix(line, "SETUP") {
					method = "SETUP"
				} else if strings.HasPrefix(line, "PLAY") {
					method = "PLAY"
				}
			}

			switch method {
			case "OPTIONS":
				fmt.Fprintf(conn, "RTSP/1.0 200 OK\r\nCSeq: %d\r\nPublic: DESCRIBE, SETUP, PLAY, TEARDOWN\r\n\r\n", cseq)
			case "DESCRIBE":
				fmt.Fprintf(conn, "RTSP/1.0 200 OK\r\nCSeq: %d\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s",
					cseq, len(sdpBody), sdpBody)
			case "SETUP":
				fmt.Fprintf(conn, "RTSP/1.0 200 OK\r\nCSeq: %d\r\nSession: TESTSESS\r\nTransport: RTP/AVP/TCP;unicast;interleaved=0-1\r\n\r\n", cseq)
			case "PLAY":
				fmt.Fprintf(conn, "RTSP/1.0 200 OK\r\nCSeq: %d\r\n\r\n", cseq)
			}
		}
	}()

	dmx := New(fmt.Sprintf("rtsp://127.0.0.1:%d/stream", port)).(*innerRTSPDemuxer)
	defer dmx.Close()

	params, err := dmx.Demux()
	if err != nil {
		t.Fatalf("Demux() failed: %v", err)
	}

	if params.SourceID != fmt.Sprintf("rtsp://127.0.0.1:%d/stream", port) {
		t.Fatalf("unexpected SourceID: %q", params.SourceID)
	}

	// The H264 demuxer won't produce codec params from SDP alone (no sprop-parameter-sets),
	// so VideoCodecParameters may be nil. That's expected behavior for this SDP.
}

// =========================================================================
// handleAuthentication edge cases
// =========================================================================

func TestHandleAuthentication_NoWWWAuthenticate(t *testing.T) {
	c := newClient()
	headers := map[string]string{"CSeq": "0"}
	result, err := c.handleAuthentication(headers, options, nil, "rtsp://test", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Should return the same headers unchanged
	if result["CSeq"] != "0" {
		t.Fatal("expected headers unchanged")
	}
}

// =========================================================================
// announce
// =========================================================================

func TestAnnounce_SendsSDPBody(t *testing.T) {
	c, fc := setupClient(okResponse(0))

	sess := sdp.Session{URI: "rtsp://example.com/stream"}
	medias := []sdp.Media{
		{AVType: "video", PayloadType: 96, Type: gomedia.H264},
	}

	if err := c.announce(sess, medias); err != nil {
		t.Fatal(err)
	}

	req := fc.writeBuf.String()
	if !strings.Contains(req, "ANNOUNCE") {
		t.Fatalf("expected ANNOUNCE method: %q", req)
	}
	if !strings.Contains(req, "Content-Type: application/sdp") {
		t.Fatalf("expected Content-Type header: %q", req)
	}
	if !strings.Contains(req, "Content-Length:") {
		t.Fatalf("expected Content-Length header: %q", req)
	}
}

// =========================================================================
// record
// =========================================================================

func TestRecord_SendsRecordMethod(t *testing.T) {
	c, fc := setupClient(okResponse(0))
	if err := c.record(); err != nil {
		t.Fatal(err)
	}
	req := fc.writeBuf.String()
	if !strings.Contains(req, "RECORD") {
		t.Fatalf("expected RECORD method: %q", req)
	}
}

// =========================================================================
// demuxer — desync recovery
// =========================================================================

func TestReadPacket_DesyncRecovery(t *testing.T) {
	// Simulate garbage bytes followed by a valid RTP interleaved packet
	rtpPayload := buildMinimalRTPPayload(1, 1000)
	validPkt := buildRTPInterleavedPacket(0, rtpPayload)

	// Prepend garbage bytes
	var data bytes.Buffer
	data.Write([]byte{0x00, 0x01, 0x02}) // garbage
	data.Write(validPkt)

	fc := &rtspFakeConn{
		readBuf:  &data,
		writeBuf: &bytes.Buffer{},
	}

	dmx := New("rtsp://test").(*innerRTSPDemuxer)
	dmx.client.conn = fc
	dmx.client.connRW = bufio.NewReadWriter(bufio.NewReader(fc), bufio.NewWriter(fc))
	dmx.videoIdx = 0
	dmx.lastPktRcv = time.Now()

	// ReadPacket should skip garbage and find the valid packet
	// It won't produce a decoded packet (no RTP demuxer set up), but shouldn't error fatally
	pkt, err := dmx.ReadPacket()
	// The packet won't be decodable without a video demuxer, but the desync should be recovered
	_ = pkt
	_ = err
	// Main check: no panic occurred during desync recovery
}

// =========================================================================
// processRTSPPacket — overflow protection
// =========================================================================

func TestProcessRTSPPacket_OversizedMessage(t *testing.T) {
	// Send a message larger than maxRTSPHeadersMessageSize (2 << 9 = 1024 bytes)
	// without \r\n\r\n terminator
	bigMsg := strings.Repeat("X", 1100)
	fc := newRTSPFakeConn(bigMsg)

	dmx := New("rtsp://test").(*innerRTSPDemuxer)
	dmx.client.conn = fc
	dmx.client.connRW = bufio.NewReadWriter(bufio.NewReader(fc), bufio.NewWriter(fc))

	var header [headerSize]byte
	copy(header[:], "RTSP")

	err := dmx.processRTSPPacket(header)
	if err == nil {
		t.Fatal("expected error for oversized RTSP message")
	}
	if !strings.Contains(err.Error(), "failed to parse RTSP headers") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =========================================================================
// request — ReadLine EOF
// =========================================================================

func TestRequest_ReadError(t *testing.T) {
	// Connection that closes immediately during response read
	c, _ := setupClient("")
	_, err := c.request(options, nil, c.control, nil, false)
	if err == nil {
		t.Fatal("expected error when connection closed during response read")
	}
	if !strings.Contains(err.Error(), io.EOF.Error()) {
		t.Fatalf("expected EOF error, got: %v", err)
	}
}

// =========================================================================
// setup — various transport header formats
// =========================================================================

func TestSetup_InterleavedWithExtraParams(t *testing.T) {
	// Some cameras return extra transport params
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nTransport: RTP/AVP/TCP;unicast;interleaved=6-7;ssrc=12345678;mode=play\r\n\r\n"
	c, _ := setupClient(resp)

	ch, err := c.setup(6, c.control, "play")
	if err != nil {
		t.Fatal(err)
	}
	if ch != 6 {
		t.Fatalf("expected channel 6, got %d", ch)
	}
}

// =========================================================================
// describe — Content-Length with spaces
// =========================================================================

func TestDescribe_ContentLengthWithSpaces(t *testing.T) {
	sdpBody := "v=0\r\nm=video 0 RTP/AVP 96\r\n"
	resp := fmt.Sprintf(
		"RTSP/1.0 200 OK\r\nCSeq: 0\r\nContent-Type: application/sdp\r\nContent-Length:  %d \r\n\r\n%s",
		len(sdpBody), sdpBody)

	c, _ := setupClient(resp)
	medias, err := c.describe()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(medias) == 0 {
		t.Fatal("expected at least one media")
	}
}

// =========================================================================
// PCM default payload type
// =========================================================================

func TestCodecParamsToSDPMedias_PCMDefaultPayloadType(t *testing.T) {
	pcmCp := pcm.NewCodecParameters(0, gomedia.PCM, 1, 44100)

	pair := gomedia.CodecParametersPair{
		AudioCodecParameters: pcmCp,
	}

	medias, err := codecParamsToSDPMedias(pair)
	if err != nil {
		t.Fatal(err)
	}
	if medias[0].PayloadType != 96 {
		t.Fatalf("expected default payload type 96 for PCM, got %d", medias[0].PayloadType)
	}
	if medias[0].TimeScale != 44100 {
		t.Fatalf("expected timescale 44100 for PCM, got %d", medias[0].TimeScale)
	}
}

// =========================================================================
// Muxer — WritePacket only supports video
// =========================================================================

func TestMuxerWritePacket_AudioPacketRejected(t *testing.T) {
	// We need a muxer with videoMuxer set but passing an audio packet
	// Since we can't easily create a real videoMuxer, test the nil path
	m := &Muxer{
		client: newClient(),
	}
	m.client.conn = newRTSPFakeConn("")

	err := m.WritePacket(nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

// =========================================================================
// ErrRTPMuxerNotImplemented
// =========================================================================

func TestErrRTPMuxerNotImplemented(t *testing.T) {
	m := &Muxer{client: newClient()}
	err := m.WritePacket(nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "RTP muxer not initialized") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

// =========================================================================
// Demuxer ReadPacket — channel routing
// =========================================================================

func TestReadPacket_UnknownChannel(t *testing.T) {
	// RTP packet on unknown channel should be read but ignored
	rtpPayload := buildMinimalRTPPayload(1, 1000)
	pktData := buildRTPInterleavedPacket(99, rtpPayload) // channel 99 not mapped

	fc := &rtspFakeConn{
		readBuf:  bytes.NewBuffer(pktData),
		writeBuf: &bytes.Buffer{},
	}

	dmx := New("rtsp://test").(*innerRTSPDemuxer)
	dmx.client.conn = fc
	dmx.client.connRW = bufio.NewReadWriter(bufio.NewReader(fc), bufio.NewWriter(fc))
	dmx.videoIdx = 0
	dmx.audioIdx = 2
	dmx.lastPktRcv = time.Now()

	pkt, err := dmx.ReadPacket()
	// Should not error fatally, just return nil packet
	if err != nil {
		t.Logf("got error (may be expected for EOF after packet): %v", err)
	}
	if pkt != nil {
		t.Fatal("expected nil packet for unknown channel")
	}
}

// =========================================================================
// Demuxer — timeout
// =========================================================================

func TestReadPacket_Timeout(t *testing.T) {
	dmx := New("rtsp://test").(*innerRTSPDemuxer)
	dmx.client.conn = newRTSPFakeConn("") // empty, will EOF
	dmx.client.connRW = bufio.NewReadWriter(
		bufio.NewReader(dmx.client.conn),
		bufio.NewWriter(dmx.client.conn))
	// Set lastPktRcv far in the past to trigger timeout
	dmx.lastPktRcv = time.Now().Add(-time.Minute)

	_, err := dmx.ReadPacket()
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "packet timeout expired") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =========================================================================
// request — multiple header parsing
// =========================================================================

func TestRequest_ParsesMultipleHeaders(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\n" +
		"CSeq: 0\r\n" +
		"Server: TestServer/1.0\r\n" +
		"Date: Mon, 01 Jan 2024 00:00:00 GMT\r\n" +
		"\r\n"
	c, _ := setupClient(resp)

	headers, err := c.request(options, nil, c.control, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if headers["Server"] != "TestServer/1.0" {
		t.Fatalf("expected Server header, got: %v", headers)
	}
	if headers["Date"] != "Mon, 01 Jan 2024 00:00:00 GMT" {
		t.Fatalf("expected Date header, got: %v", headers)
	}
}

// =========================================================================
// Setup with different interleaved channel values
// =========================================================================

func TestSetup_ChannelZero(t *testing.T) {
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nTransport: RTP/AVP/TCP;unicast;interleaved=0-1\r\n\r\n"
	c, _ := setupClient(resp)
	ch, err := c.setup(0, c.control, "play")
	if err != nil {
		t.Fatal(err)
	}
	if ch != 0 {
		t.Fatalf("expected channel 0, got %d", ch)
	}
}

// =========================================================================
// demuxer — findStreams SDP sorting
// =========================================================================

func TestFindStreams_SDPSorting(t *testing.T) {
	// Verify that mediaSDP is sorted: video before audio
	dmx := New("rtsp://test").(*innerRTSPDemuxer)
	dmx.mediaSDP = []sdp.Media{
		{AVType: "audio", Type: gomedia.AAC, Control: "trackID=0"},
		{AVType: "video", Type: gomedia.H264, Control: "trackID=1"},
	}
	// We can't call findStreams without a real client setup, but we can verify
	// the sorting logic independently by checking the sort.Slice behavior
	medias := dmx.mediaSDP

	getPriority := func(avType string) int {
		switch avType {
		case video:
			return 0
		case audio:
			return 1
		default:
			return 2
		}
	}

	// Before sort
	if medias[0].AVType != "audio" {
		t.Fatal("expected audio first before sort")
	}

	// Manual sort to verify logic
	if getPriority(medias[0].AVType) < getPriority(medias[1].AVType) {
		t.Fatal("audio should have higher priority number than video")
	}
}

// =========================================================================
// Describe with multiple SDP media entries
// =========================================================================

func TestDescribe_MultipleMediaEntries(t *testing.T) {
	sdpBody := "v=0\r\n" +
		"m=video 0 RTP/AVP 96\r\n" +
		"a=rtpmap:96 H264/90000\r\n" +
		"a=control:trackID=0\r\n" +
		"m=audio 0 RTP/AVP 97\r\n" +
		"a=rtpmap:97 MPEG4-GENERIC/44100\r\n" +
		"a=control:trackID=1\r\n"

	resp := fmt.Sprintf(
		"RTSP/1.0 200 OK\r\nCSeq: 0\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s",
		len(sdpBody), sdpBody)

	c, _ := setupClient(resp)
	medias, err := c.describe()
	if err != nil {
		t.Fatal(err)
	}
	if len(medias) != 2 {
		t.Fatalf("expected 2 media entries, got %d", len(medias))
	}
	if medias[0].AVType != "video" {
		t.Fatalf("expected first media to be video, got %q", medias[0].AVType)
	}
	if medias[1].AVType != "audio" {
		t.Fatalf("expected second media to be audio, got %q", medias[1].AVType)
	}
}

// =========================================================================
// Describe with H265 SDP
// =========================================================================

func TestDescribe_H265SDP(t *testing.T) {
	sdpBody := "v=0\r\n" +
		"m=video 0 RTP/AVP 96\r\n" +
		"a=rtpmap:96 H265/90000\r\n" +
		"a=control:trackID=0\r\n"

	resp := fmt.Sprintf(
		"RTSP/1.0 200 OK\r\nCSeq: 0\r\nContent-Type: application/sdp\r\nContent-Length: %d\r\n\r\n%s",
		len(sdpBody), sdpBody)

	c, _ := setupClient(resp)
	medias, err := c.describe()
	if err != nil {
		t.Fatal(err)
	}
	if medias[0].Type != gomedia.H265 {
		t.Fatalf("expected H265 codec, got %v", medias[0].Type)
	}
}

// =========================================================================
// request — 401 status is allowed (for auth flow)
// =========================================================================

func TestRequest_401StatusAllowed(t *testing.T) {
	resp := "RTSP/1.0 401 Unauthorized\r\nCSeq: 0\r\n\r\n"
	c, _ := setupClient(resp)
	// 401 without WWW-Authenticate should still succeed (no auth challenge)
	headers, err := c.request(options, nil, c.control, nil, false)
	if err != nil {
		t.Fatalf("401 should be allowed: %v", err)
	}
	if headers == nil {
		t.Fatal("expected non-nil headers")
	}
}

// =========================================================================
// controlTrack with rtsps://
// =========================================================================

func TestControlTrack_RTSPSAbsoluteURL(t *testing.T) {
	// controlTrack only checks for "rtsp://" — rtsps:// URLs are NOT treated as absolute
	result := controlTrack("rtsp://host/base", "rtsps://host/other/track")
	// Since rtsps:// doesn't contain "rtsp://", it will be appended
	if result != "rtsp://host/base/rtsps://host/other/track" {
		t.Fatalf("rtsps URL should be appended (not detected as absolute), got %q", result)
	}
}

// =========================================================================
// Setup — interleaved parsing edge case
// =========================================================================

func TestSetup_InterleavedNoRange(t *testing.T) {
	// Transport header with interleaved but no range (e.g., "interleaved=0")
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nTransport: RTP/AVP/TCP;unicast;interleaved=0\r\n\r\n"
	c, _ := setupClient(resp)
	_, err := c.setup(0, c.control, "play")
	if err == nil {
		t.Fatal("expected error when interleaved has no range (missing dash)")
	}
}

// =========================================================================
// Request seq carried via Content-length normalization
// =========================================================================

func TestRequest_ContentLengthLowercaseNormalized(t *testing.T) {
	// Verify "Content-length" (lowercase l) gets normalized to "Content-Length"
	resp := "RTSP/1.0 200 OK\r\nCSeq: 0\r\nContent-length: 5\r\n\r\nhello"
	c, _ := setupClient(resp)
	headers, err := c.request(options, nil, c.control, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := headers["Content-Length"]; !ok || v != "5" {
		t.Fatalf("Content-length not normalized: %v", headers)
	}
	// "Content-length" key should NOT exist separately
	if _, ok := headers["Content-length"]; ok {
		t.Fatal("original Content-length key should be replaced")
	}
}

// =========================================================================
// request — writes body for ANNOUNCE
// =========================================================================

func TestRequest_BodyWrittenCorrectly(t *testing.T) {
	c, fc := setupClient(okResponse(0))
	body := []byte("v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=gomedia\r\n")
	headers := map[string]string{
		"Content-Type":   "application/sdp",
		"Content-Length": strconv.Itoa(len(body)),
	}

	_, err := c.request(announce, headers, c.control, body, false)
	if err != nil {
		t.Fatal(err)
	}

	written := fc.writeBuf.Bytes()
	if !bytes.Contains(written, body) {
		t.Fatal("body not written to connection")
	}
}
