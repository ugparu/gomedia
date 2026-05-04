//nolint:gosec,mnd //tls is unused, fix in future
package rtsp

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ugparu/gomedia/utils/buffer"
	"github.com/ugparu/gomedia/utils/logger"
	"github.com/ugparu/gomedia/utils/sdp"
)

// client holds the state of one RTSP control channel: connection, sequence
// counter, Digest/Basic auth material, and the negotiated session/control URIs.
type client struct {
	seq      uint
	connRW   *bufio.ReadWriter
	pURL     *url.URL
	conn     net.Conn
	control  string // Content-Base / session control URI (updated by the server).
	session  string
	realm    string // populated from WWW-Authenticate on the first 401.
	nonce    string
	username string
	password string
	name     string
	url      string
	headers  map[string]string
	methods  map[rtspMethod]bool // populated from OPTIONS → Public.
	log      logger.Logger
}

func newClient() *client {
	return &client{
		seq:      0,
		connRW:   nil,
		pURL:     &url.URL{},
		conn:     nil,
		control:  "",
		session:  "",
		realm:    "",
		nonce:    "",
		username: "",
		password: "",
		name:     "",
		url:      "",
		headers:  map[string]string{"User-Agent": "gomedia"},
		log:      logger.Default,
		methods: map[rtspMethod]bool{
			describe:     false,
			announce:     false,
			getParameter: false,
			options:      false,
			pause:        false,
			record:       false,
			teardown:     false,
			play:         false,
			setup:        false,
			setParameter: false,
			redirect:     false,
		},
	}
}

// establishConnection dials the RTSP server, optionally upgrades to TLS for
// rtsps://, and issues an initial OPTIONS to populate c.methods.
func (c *client) establishConnection(rawURL string) (err error) {
	c.url = rawURL

	l, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	var username, password string
	if l.User != nil {
		username = l.User.Username()
		password, _ = l.User.Password()
		l.User = nil
	}

	if l.Port() == "" {
		l.Host = fmt.Sprintf("%s:%s", l.Host, "554") // default RTSP port
	}

	if l.Scheme != RTSP && l.Scheme != RTSPS {
		l.Scheme = RTSP
	}

	c.pURL = l
	c.username = username
	c.password = password
	c.control = l.String()

	if c.conn, err = net.DialTimeout("tcp", c.pURL.Host, dialTimeout); err != nil {
		return err
	}

	if err = c.conn.SetDeadline(time.Now().Add(readWriteTimeout)); err != nil {
		return err
	}

	if c.pURL.Scheme == "rtsps" {
		tlsConfig := &tls.Config{InsecureSkipVerify: true, ServerName: c.pURL.Hostname()} //nolint: exhaustruct
		tlsConn := tls.Client(c.conn, tlsConfig)
		if err = tlsConn.Handshake(); err != nil {
			return err
		}
		c.conn = tlsConn
	}

	c.connRW = bufio.NewReadWriter(bufio.NewReaderSize(c.conn, tcpBufSize), bufio.NewWriterSize(c.conn, tcpBufSize))

	if err = c.options(); err != nil {
		return err
	}

	c.log.Debug(c, "RTSP session set up")

	return nil
}

// request serializes and sends an RTSP request, optionally appending a body,
// and parses the response headers. When nores is true the call returns after
// flushing (used for TEARDOWN and similar fire-and-forget methods). A 401 with
// WWW-Authenticate triggers handleAuthentication, which retries the request once
// with computed Digest/Basic credentials.
func (c *client) request(method rtspMethod,
	customHeaders map[string]string, uri string, body []byte, nores bool) (resp map[string]string, err error) {
	builder := bytes.Buffer{}
	builder.WriteString(fmt.Sprintf("%s %s RTSP/1.0\r\n", method, uri))
	builder.WriteString(fmt.Sprintf("CSeq: %d\r\n", c.seq))

	if c.realm != "" {
		// Digest per RFC 2617: H(A1) / H(A2) / response = H(H(A1):nonce:H(A2)).
		md5UserRealmPwd := fmt.Sprintf("%x", md5.Sum(fmt.Appendf(nil, "%s:%s:%s", c.username, c.realm, c.password)))
		md5MethodURL := fmt.Sprintf("%x", md5.Sum(fmt.Appendf(nil, "%s:%s", method, uri)))
		response := fmt.Sprintf("%x", md5.Sum(fmt.Appendf(nil, "%s:%s:%s", md5UserRealmPwd, c.nonce, md5MethodURL)))
		authorization := fmt.Sprintf("Digest username=\"%s\", realm=\"%s\", nonce=\"%s\", uri=\"%s\", response=\"%s\"",
			c.username, c.realm, c.nonce, uri, response)
		builder.WriteString(fmt.Sprintf("Authorization: %s\r\n", authorization))
	}

	for k, v := range customHeaders {
		builder.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}

	for k, v := range c.headers {
		builder.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}

	builder.WriteString("\r\n")

	if err = c.conn.SetWriteDeadline(time.Now().Add(readWriteTimeout)); err != nil {
		return nil, err
	}

	if _, err = c.connRW.WriteString(builder.String()); err != nil {
		return nil, err
	}

	if len(body) > 0 {
		if _, err = c.connRW.Write(body); err != nil {
			return nil, err
		}
	}

	if err = c.connRW.Flush(); err != nil {
		return nil, err
	}

	defer func() {
		c.seq++
	}()

	if nores {
		return
	}

	var line []byte
	var responseStatus string
	responseHeaders := make(map[string]string)

	for {
		if err = c.conn.SetReadDeadline(time.Now().Add(readWriteTimeout)); err != nil {
			return nil, err
		}

		if line, _, err = c.connRW.ReadLine(); err != nil {
			return nil, err
		}

		if len(line) == 0 {
			break
		}

		if strings.Contains(string(line), "RTSP/1.0") {
			responseStatus = string(line)
		}

		splits := strings.SplitN(string(line), ":", 2)
		if len(splits) != 1 {
			if splits[0] == "Content-length" {
				splits[0] = "Content-Length"
			}
			responseHeaders[splits[0]] = strings.Trim(splits[1], " ")
		}
	}

	if val, ok := responseHeaders["CSeq"]; ok && val != strconv.FormatUint(uint64(c.seq), 10) {
		return nil, fmt.Errorf("response seq mismatch %v!=%v", c.seq, val)
	}

	if _, ok := responseHeaders["WWW-Authenticate"]; ok {
		responseHeaders, err = c.handleAuthentication(responseHeaders, method, customHeaders, uri, body)
		if err != nil {
			return nil, err
		}
	}

	if val, ok := responseHeaders["Session"]; ok {
		splits2 := strings.Split(val, ";")
		c.session = strings.TrimSpace(splits2[0])
		c.headers["Session"] = strings.TrimSpace(splits2[0])
	}

	if val, ok := responseHeaders["Content-Base"]; ok {
		c.control = strings.TrimSpace(val)
	}

	if !strings.HasPrefix(responseStatus, "RTSP/1.0 200") && !strings.HasPrefix(responseStatus, "RTSP/1.0 401") {
		return nil, errors.New("camera send status: " + responseStatus)
	}

	return responseHeaders, nil
}

// handleAuthentication extracts Digest or Basic challenge material from
// WWW-Authenticate and retries the original request once. A second 401 is
// treated as a permanent failure to avoid infinite loops.
func (c *client) handleAuthentication(
	responseHeaders map[string]string,
	method rtspMethod,
	customHeaders map[string]string,
	uri string,
	body []byte,
) (map[string]string, error) {
	val, ok := responseHeaders["WWW-Authenticate"]
	if !ok {
		return responseHeaders, nil
	}

	if strings.Contains(val, "Digest") {
		if c.realm != "" {
			return nil, errors.New("401 unauthorized")
		}
		c.realm = stringInBetween(val, "realm=\"", "\"")
		c.nonce = stringInBetween(val, "nonce=\"", "\"")
	} else if strings.Contains(val, "Basic") {
		if _, ok = c.headers["Authorization"]; ok {
			return nil, errors.New("401 unauthorized")
		}
		c.headers["Authorization"] = "Basic " +
			base64.StdEncoding.EncodeToString([]byte(c.username+":"+c.password))
	}

	return c.request(method, customHeaders, uri, body, false)
}

// supportsPublish reports whether OPTIONS advertised the ANNOUNCE+RECORD pair
// needed to act as a publishing client.
func (c *client) supportsPublish() bool {
	return c.methods[announce] && c.methods[record]
}

func (c *client) options() (err error) {
	c.log.Debug(c, "Processing options request")

	resp, err := c.request(options, nil, c.control, nil, false)
	if err != nil {
		return err
	}

	if val, ok := resp["Public"]; ok {
		c.log.Debugf(c, "Supported methods: %s", val)
		for m := range strings.SplitSeq(val, ",") {
			c.methods[rtspMethod(strings.TrimSpace(m))] = true
		}
	}

	return nil
}

// describe issues DESCRIBE and returns the parsed SDP media descriptions.
func (c *client) describe() (sdps []sdp.Media, err error) {
	c.log.Debug(c, "Processing describe request")

	resp, err := c.request(describe, map[string]string{"Accept": "application/sdp"}, c.control, nil, false)
	if err != nil {
		return nil, err
	}

	if val, ok := resp["Content-Type"]; !ok || val != "application/sdp" {
		return nil, fmt.Errorf("wrong content type %v", val)
	}

	val, ok := resp["Content-Length"]
	if !ok {
		return nil, errors.New("no content length")
	}
	var contentLen int
	if contentLen, err = strconv.Atoi(strings.TrimSpace(val)); err != nil {
		return nil, err
	}

	sdpBuffer := buffer.Get(contentLen)

	if err = c.conn.SetReadDeadline(time.Now().Add(readWriteTimeout)); err != nil {
		return nil, err
	}
	if _, err = io.ReadFull(c.connRW, sdpBuffer.Data()); err != nil {
		return nil, err
	}
	_, sdps = sdp.Parse(string(sdpBuffer.Data()))

	return sdps, nil
}

// setup performs SETUP for one media stream over RTP/AVP/TCP interleaved on
// channels chTMP / chTMP+1 and returns the channel the server actually chose.
func (c *client) setup(chTMP int, uri string, mode string) (streamIdx int, err error) {
	c.log.Debug(c, "Processing setup request")

	headers := map[string]string{"Transport": fmt.Sprintf("RTP/AVP/TCP;unicast;interleaved=%d-%d;mode=%s", chTMP, chTMP+1, mode)}

	c.log.Debugf(c, "Setting up stream with URI: %s", uri)
	c.log.Debugf(c, "Headers: %+v", headers)

	resp, err := c.request(setup, headers, uri, nil, false)
	if err != nil {
		return -1, err
	}

	val, ok := resp["Transport"]
	if !ok {
		return -1, errors.New("no transport header")
	}

	if !strings.Contains(val, "interleaved") {
		return -1, errors.New("no interleaved")
	}

	for vs := range strings.SplitSeq(val, ";") {
		if !strings.Contains(vs, "interleaved") {
			continue
		}
		splits3 := strings.Split(vs, "=")
		if len(splits3) == 2 {
			splits4 := strings.Split(splits3[1], "-")
			if len(splits4) == 2 {
				var val int
				if val, err = strconv.Atoi(splits4[0]); err != nil {
					return -1, err
				}
				return val, nil
			}
		}
	}

	return -1, errors.New("no interleaved")
}

func (c *client) play() (err error) {
	c.log.Debug(c, "Processing play request")

	if _, err = c.request(play, nil, c.control, nil, false); err != nil {
		return err
	}

	return nil
}

// announce publishes the outgoing SDP (used when this client is acting as a publisher).
func (c *client) announce(sess sdp.Session, medias []sdp.Media) (err error) {
	c.log.Debug(c, "Processing announce request")

	bodyStr := sdp.Generate(sess, medias)
	body := []byte(bodyStr)

	headers := map[string]string{
		"Content-Type":   "application/sdp",
		"Content-Length": strconv.Itoa(len(body)),
	}

	if _, err = c.request(announce, headers, c.control, body, false); err != nil {
		return err
	}

	return nil
}

func (c *client) record() (err error) {
	c.log.Debug(c, "Processing record request")

	if _, err = c.request(record, nil, c.control, nil, false); err != nil {
		return err
	}

	return nil
}

// ping sends a fire-and-forget OPTIONS to keep the connection alive between
// long periods of silence (some cameras drop idle sessions).
func (c *client) ping() (err error) {
	c.log.Debug(c, "Processing ping request")

	if _, err = c.request(options, nil, c.control, nil, true); err != nil {
		return err
	}

	return nil
}

// Read fills buf from the RTSP connection with a per-call deadline; used by
// consumers that want to drain interleaved data outside of request().
func (c *client) Read(buf []byte) (err error) {
	if c.conn == nil {
		return errors.New("connection is not opened")
	}
	if err = c.conn.SetDeadline(time.Now().Add(readWriteTimeout)); err != nil {
		return err
	}

	if len(buf) > 0 {
		if err = c.conn.SetReadDeadline(time.Now().Add(readWriteTimeout)); err != nil {
			return err
		}

		if _, err = io.ReadFull(c.connRW, buf); err != nil {
			return err
		}
		return nil
	}

	return
}

// Close tries a best-effort TEARDOWN and then closes the TCP/TLS connection.
// Errors are logged but not returned — Close is called from defer paths.
func (c *client) Close() {
	if c.conn != nil {
		if err := c.conn.SetDeadline(time.Now().Add(readWriteTimeout)); err == nil {
			if _, err = c.request(teardown, nil, c.control, nil, true); err != nil {
				c.log.Debugf(c, "Teardown error: %v", err)
			}
		}

		if err := c.conn.Close(); err != nil {
			c.log.Debugf(c, "Connection close error: %v", err)
		}
	}
}

func (c *client) String() string {
	return fmt.Sprintf("RTSP_CLIENT url=%s", c.pURL.String())
}
