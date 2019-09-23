package lb

import "net/http"

type httpReqDesc struct {
	feConn            *bufConn
	feStatusLine      string
	feStatusLineParts []string
	feHdr             http.Header
	feHost            string
	fePath            string
	beName            string
	beConn            *bufConn
	beStatusLine      string
	beStatusLineParts []string
	beHdr             http.Header
	err               error
}
