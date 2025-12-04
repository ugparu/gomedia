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

// client represents an RTSP client.
type client struct {
	seq      uint                // Sequence number for RTSP requests.
	connRW   *bufio.ReadWriter   // Buffered read and write interface for the connection.
	pURL     *url.URL            // Parsed URL for the RTSP connection.
	conn     net.Conn            // The underlying TCP connection.
	control  string              // Control information for the RTSP session.
	session  string              // RTSP session identifier.
	realm    string              // RTSP realm for authentication.
	nonce    string              // Nonce value for authentication.
	username string              // Username for authentication.
	password string              // Password for authentication.
	name     string              // Name associated with the client.
	url      string              // Raw URL for the RTSP connection.
	headers  map[string]string   // Headers to be included in RTSP requests.
	methods  map[rtspMethod]bool // Supported RTSP methods and their availability.
}

// newClient creates a new instance of the RTSP client with default values.
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
		headers:  map[string]string{"User-Agent": "CubicCV"},
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

// establishConnection establishes an RTSP connection based on the provided raw URL.
func (c *client) establishConnection(rawURL string) (err error) {
	c.url = rawURL

	// Parse the raw URL to extract relevant information.
	l, err := url.Parse(rawURL)
	if err != nil {
		return err
	}
	username := l.User.Username()
	password, _ := l.User.Password()
	l.User = nil

	// If the port is not specified, set it to the default RTSP port (554).
	if l.Port() == "" {
		l.Host = fmt.Sprintf("%s:%s", l.Host, "554")
	}

	// Ensure that the URL scheme is either RTSP or RTSPS.
	if l.Scheme != RTSP && l.Scheme != RTSPS {
		l.Scheme = RTSP
	}

	// Update client fields with parsed information.
	c.pURL = l
	c.username = username
	c.password = password
	c.control = l.String()

	// Establish a TCP connection with the specified timeout.
	if c.conn, err = net.DialTimeout("tcp", c.pURL.Host, dialTimeout); err != nil {
		return err
	}

	// Set a deadline for read and write operations on the connection.
	if err = c.conn.SetDeadline(time.Now().Add(readWriteTimeout)); err != nil {
		return err
	}

	// If the URL scheme is RTSPS, perform a TLS handshake.
	if c.pURL.Scheme == "rtsps" {
		tlsConfig := &tls.Config{InsecureSkipVerify: true, ServerName: c.pURL.Hostname()} //nolint: exhaustruct
		tlsConn := tls.Client(c.conn, tlsConfig)
		if err = tlsConn.Handshake(); err != nil {
			return err
		}
		c.conn = tlsConn
	}

	// Create a buffered read and write interface for the connection.
	c.connRW = bufio.NewReadWriter(bufio.NewReaderSize(c.conn, tcpBufSize), bufio.NewWriterSize(c.conn, tcpBufSize))

	// Perform an OPTIONS request as part of connection setup.
	if err = c.options(); err != nil {
		return err
	}

	// Log a debug message indicating successful RTSP session setup.
	logger.Debug(c, "RTSP session set up")

	return nil
}

// request sends an RTSP request with the specified method, custom headers, URI, and option to skip response.
// It returns the RTSP response headers as a map[string]string and any encountered error.
func (c *client) request(method rtspMethod,
	customHeaders map[string]string, uri string, nores bool) (resp map[string]string, err error) {
	// Prepare the RTSP request string.
	builder := bytes.Buffer{}
	builder.WriteString(fmt.Sprintf("%s %s RTSP/1.0\r\n", method, uri))
	builder.WriteString(fmt.Sprintf("CSeq: %d\r\n", c.seq))

	// Include Digest authentication details if realm is available.
	if c.realm != "" {
		// Calculate MD5 hashes for authentication.
		md5UserRealmPwd := fmt.Sprintf("%x", md5.Sum(fmt.Appendf(nil, "%s:%s:%s", c.username, c.realm, c.password)))
		md5MethodURL := fmt.Sprintf("%x", md5.Sum(fmt.Appendf(nil, "%s:%s", method, uri)))
		response := fmt.Sprintf("%x", md5.Sum(fmt.Appendf(nil, "%s:%s:%s", md5UserRealmPwd, c.nonce, md5MethodURL)))
		authorization := fmt.Sprintf("Digest username=\"%s\", realm=\"%s\", nonce=\"%s\", uri=\"%s\", response=\"%s\"",
			c.username, c.realm, c.nonce, uri, response)
		builder.WriteString(fmt.Sprintf("Authorization: %s\r\n", authorization))
	}

	// Include custom headers.
	for k, v := range customHeaders {
		builder.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}

	// Include client headers.
	for k, v := range c.headers {
		builder.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}

	// End the request headers.
	builder.WriteString("\r\n")

	requestString := builder.String()

	// Set write deadline for the connection.
	if err = c.conn.SetWriteDeadline(time.Now().Add(readWriteTimeout)); err != nil {
		return
	}

	// Write the request to the connection.
	if _, err = c.connRW.WriteString(requestString); err != nil {
		return nil, err
	}

	// Flush the buffered writer to send the request.
	if err = c.connRW.Flush(); err != nil {
		return nil, err
	}

	// Defer incrementing the sequence number until after sending the request.
	defer func() {
		c.seq++
	}()

	// If no response is expected, return.
	if nores {
		return
	}

	// Initialize variables for reading the response.
	var line []byte
	var responseStatus string
	responseHeaders := make(map[string]string)

	// Read and process each line of the response.
	for {
		// Set read deadline for the connection.
		if err = c.conn.SetReadDeadline(time.Now().Add(readWriteTimeout)); err != nil {
			return
		}

		// Read a line from the response.
		if line, _, err = c.connRW.ReadLine(); err != nil {
			return nil, err
		}

		// Break loop if an empty line is encountered.
		if len(line) == 0 {
			break
		}

		// Check for unexpected status codes in the response.
		if strings.Contains(string(line), "RTSP/1.0") {
			responseStatus = string(line)
		}

		// Split the line into key-value pairs and update the response headers.
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

	// Process authentication challenges in the response.
	if _, ok := responseHeaders["WWW-Authenticate"]; ok {
		responseHeaders, err = c.handleAuthentication(responseHeaders, method, customHeaders, uri)
		if err != nil {
			return nil, err
		}
	}

	// Extract session information from the response.
	if val, ok := responseHeaders["Session"]; ok {
		splits2 := strings.Split(val, ";")
		c.session = strings.TrimSpace(splits2[0])
		c.headers["Session"] = strings.TrimSpace(splits2[0])
	}

	// Update control information based on the response.
	if val, ok := responseHeaders["Content-Base"]; ok {
		c.control = strings.TrimSpace(val)
	}

	if !strings.HasPrefix(responseStatus, "RTSP/1.0 200") && !strings.HasPrefix(responseStatus, "RTSP/1.0 401") {
		return nil, errors.New("camera send status: " + responseStatus)
	}

	return responseHeaders, nil
}

// handleAuthentication processes authentication challenges in the response
// and returns updated response headers if authentication was needed.
func (c *client) handleAuthentication(
	responseHeaders map[string]string,
	method rtspMethod,
	customHeaders map[string]string,
	uri string,
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

	// Resend the request with updated authentication.
	return c.request(method, customHeaders, uri, false)
}

// options sends an RTSP OPTIONS request to the server and updates the supported methods.
func (c *client) options() (err error) {
	// Log debug information.
	logger.Debug(c, "Processing options request")

	// Send OPTIONS request and retrieve response.
	resp, err := c.request(options, nil, c.control, false)
	if err != nil {
		return err
	}

	// Parse and update supported methods from the response.
	if val, ok := resp["Public"]; ok {
		logger.Debugf(c, "Supported methods: %s", val)
		for m := range strings.SplitSeq(val, ",") {
			c.methods[rtspMethod(strings.TrimSpace(m))] = true
		}
	}

	return nil
}

// describe sends an RTSP DESCRIBE request to the server and parses the SDP information from the response.
func (c *client) describe() (sdps []sdp.Media, err error) {
	// Log debug information.
	logger.Debug(c, "Processing describe request")

	// Send DESCRIBE request with "Accept" header specifying "application/sdp".
	resp, err := c.request(describe, map[string]string{"Accept": "application/sdp"}, c.control, false)
	if err != nil {
		return nil, err
	}

	// Check for the correct content type in the response.
	if val, ok := resp["Content-Type"]; !ok || val != "application/sdp" {
		return nil, fmt.Errorf("wrong content type %v", val)
	}

	// Retrieve and parse SDP information from the response.
	val, ok := resp["Content-Length"]
	if !ok {
		return nil, errors.New("no content length")
	}
	var contentLen int
	if contentLen, err = strconv.Atoi(strings.TrimSpace(val)); err != nil {
		return nil, err
	}

	sdpBuffer := buffer.Get(contentLen)
	defer sdpBuffer.Release()

	if err = c.conn.SetReadDeadline(time.Now().Add(readWriteTimeout)); err != nil {
		return
	}
	if _, err = io.ReadFull(c.connRW, sdpBuffer.Data()); err != nil {
		return nil, err
	}
	_, sdps = sdp.Parse(string(sdpBuffer.Data()))

	return sdps, nil
}

// setup sends an RTSP SETUP request to the server and retrieves the interleaved channel information.
func (c *client) setup(chTMP int, uri string) (streamIdx int, err error) {
	// Log debug information.
	logger.Debug(c, "Processing setup request")

	// Configure the "Transport" header with interleaved channel information.
	headers := map[string]string{"Transport": fmt.Sprintf("RTP/AVP/TCP;unicast;interleaved=%d-%d", chTMP, chTMP+1)}

	// Send SETUP request with specified headers and URI.
	resp, err := c.request(setup, headers, uri, false)
	if err != nil {
		return -1, err
	}

	// Retrieve and parse the "Transport" header to get interleaved channel information.
	val, ok := resp["Transport"]
	if !ok {
		return -1, errors.New("no transport header")
	}

	// Check for the presence of "interleaved" in the "Transport" header.
	if !strings.Contains(val, "interleaved") {
		return -1, errors.New("no interleaved")
	}

	// Split and parse the "Transport" header to extract the interleaved channel information.
	for vs := range strings.SplitSeq(val, ";") {
		if !strings.Contains(vs, "interleaved") {
			continue
		}
		splits3 := strings.Split(vs, "=")
		if len(splits3) == 2 { // Successfully split the channel information.
			splits4 := strings.Split(splits3[1], "-")
			if len(splits4) == 2 { // Successfully split the range.
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

// play sends an RTSP PLAY request to the server to initiate streaming.
func (c *client) play() (err error) {
	// Log debug information.
	logger.Debug(c, "Processing play request")

	// Send PLAY request to the server.
	if _, err = c.request(play, nil, c.control, false); err != nil {
		return err
	}

	return nil
}

// ping sends an RTSP GET_PARAMETER request to keep the connection alive.
func (c *client) ping() (err error) {
	// Log debug information.
	logger.Debug(c, "Processing ping request")

	// Send GET_PARAMETER request to the server (no response expected).
	if _, err = c.request(options, nil, c.control, true); err != nil {
		return err
	}

	return nil
}

// Read reads a specified number of bytes (n) from the RTSP connection.
// If n is 0, it reads the entire response until the connection is closed.
func (c *client) Read(buf []byte) (err error) {
	if c.conn == nil {
		return errors.New("connection is not opened")
	}
	// Set the deadline for the connection.
	if err = c.conn.SetDeadline(time.Now().Add(readWriteTimeout)); err != nil {
		return err
	}

	// Read a specific number of bytes if n > 0.
	if len(buf) > 0 {
		// Set the read deadline for the connection.
		if err = c.conn.SetReadDeadline(time.Now().Add(readWriteTimeout)); err != nil {
			return
		}

		if _, err = io.ReadFull(c.connRW, buf); err != nil {
			return err
		}
		return nil
	}

	return
}

// Close closes the RTSP connection.
func (c *client) Close() {
	// Check if the connection is not nil.
	if c.conn != nil {
		// Set a deadline for the connection.
		if err := c.conn.SetDeadline(time.Now().Add(readWriteTimeout)); err == nil {
			// Send TEARDOWN request to gracefully close the connection (no response expected).
			if _, err = c.request(teardown, nil, c.control, true); err != nil {
				logger.Debugf(c, "Teardown error: %v", err)
			}
		}

		// Close the underlying TCP connection.
		if err := c.conn.Close(); err != nil {
			logger.Debugf(c, "Connection close error: %v", err)
		}
	}
}

// String returns a string representation of the RTSP client.
func (c *client) String() string {
	return fmt.Sprintf("RTSP_CLIENT url=%s", c.pURL.String())
}
