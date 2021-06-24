package signal

import (
	"os"
	"os/signal"
)

var (
	singletonCh  = make(chan int)
	shutdownSigs = []os.Signal{os.Interrupt}
)

func SetupSignalHandler() (stopCh <-chan struct{}) {
	close(singletonCh) // make sure this func be called only once
	stop := make(chan struct{})
	c := make(chan os.Signal, 2)
	signal.Notify(c, shutdownSigs...)

	go func() {
		<-c
		//if receive signal, close stopCh
		close(stop)
		<-c
		os.Exit(1)
	}()
	return stop
}
