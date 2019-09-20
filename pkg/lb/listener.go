package lb

import (
	"crypto/tls"
	"net"
	"sync"

	accepter "github.com/orkunkaraduman/go-accepter"
	"github.com/pkg/errors"
)

type ListenerOptions struct {
	Network   string
	Address   string
	Handler   accepter.Handler
	TLSConfig *tls.Config
}

func (o *ListenerOptions) CopyFrom(src *ListenerOptions) {
	*o = *src
}

type Listener struct {
	opts   ListenerOptions
	accr   *accepter.Accepter
	accrMu sync.RWMutex
}

func NewListener(opts ListenerOptions) (l *Listener, err error) {
	l, err = l.Fork(opts)
	return
}

func (l *Listener) Fork(opts ListenerOptions) (ln *Listener, err error) {
	ln = &Listener{}
	ln.opts.CopyFrom(&opts)
	defer func() {
		if err == nil {
			return
		}
		ln.Close()
		ln = nil
	}()

	if l != nil {
		l.accrMu.RLock()
		defer l.accrMu.RUnlock()
		if l.accr != nil && l.accr.Handler.(*accepterHandler).SetShared(true) {
			err = errors.New("listener already forked")
			return
		}
		ln.opts.Network = l.opts.Network
		ln.opts.Address = l.opts.Address
		ln.accr = l.accr
	}

	if ln.accr == nil {
		var lis net.Listener
		lis, err = net.Listen(ln.opts.Network, ln.opts.Address)
		if err != nil {
			err = errors.WithStack(err)
			return
		}
		ln.accr = &accepter.Accepter{
			Handler: &accepterHandler{
				Lis: lis,
			},
		}
		go func(opts ListenerOptions, accr *accepter.Accepter) {
			lis := accr.Handler.(*accepterHandler).Lis
			if e := accr.Serve(lis); e != nil {
				errorLogger.Printf("listener %q serve error: %v", opts.Network+"://"+opts.Address, e)
			}
		}(ln.opts, ln.accr)
	}

	return
}

func (l *Listener) Close() {
	l.accrMu.Lock()
	if l.accr != nil {
		if !l.accr.Handler.(*accepterHandler).SetShared(false) {
			l.accr.Close()
			l.accr = nil
		}
	}
	l.accrMu.Unlock()
	return
}

func (l *Listener) GetOpts() (opts ListenerOptions) {
	opts.CopyFrom(&l.opts)
	return
}

func (l *Listener) Activate() {
	l.accrMu.RLock()
	if l.accr != nil {
		l.accr.Handler.(*accepterHandler).Set(l.opts.Handler, l.opts.TLSConfig)
	}
	l.accrMu.RUnlock()
}
