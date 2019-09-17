package lb

import "net/http"

type httpReqDesc struct {
	feConn            *bufConn
	feStatusLine      string
	feStatusLineParts []string
	feHdr             http.Header
	beConn            *bufConn
	beStatusLine      string
	beStatusLineParts []string
	beHdr             http.Header
	err               error
}
