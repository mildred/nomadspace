package waitgroup

import (
	"sync"

	"github.com/hashicorp/go-multierror"
)

type WaitGroup interface {
	Start(f Func)
	Wait() error
}

type waitGroup struct {
	wg   sync.WaitGroup
	errs chan error
}

type Func func() error

func New() WaitGroup {
	return &waitGroup{
		errs: make(chan error),
	}
}

func (wg *waitGroup) Start(f Func) {
	wg.wg.Add(1)
	go func() {
		defer wg.wg.Done()
		wg.errs <- f()
	}()
}

func (wg *waitGroup) Wait() error {
	var errRes = make(chan error)
	go func(){
		var err error
		for e := range wg.errs {
			err = multierror.Append(err, e).ErrorOrNil()
		}
		errRes <- err
	}()
	wg.wg.Wait()
	close(wg.errs)
	return <- errRes
}
