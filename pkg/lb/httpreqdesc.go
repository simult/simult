package lb

import "net/http"

type httpReqDesc struct {
	feConn       *bufConn
	feStatusLine string
	feHdr        http.Header
	beConn       *bufConn
	beStatusLine string
	beHdr        http.Header
	err          error
}
