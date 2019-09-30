package lb

import "net/http"

type httpReqDesc struct {
	feName          string
	feConn          *bufConn
	feStatusLine    string
	feStatusMethod  string
	feStatusURI     string
	feStatusVersion string
	feHdr           http.Header
	feHost          string
	fePath          string
	beName          string
	beServer        *backendServer
	beConn          *bufConn
	beStatusLine    string
	beStatusVersion string
	beStatusCode    string
	beStatusMsg     string
	beHdr           http.Header
}
