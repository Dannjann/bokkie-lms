//go:build windows

package discovery

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// advertise registers through Windows' own mDNS responder, and runs ours only
// where there is none.
//
// This file used to say Windows has no system responder holding port 5353. That
// stopped being true with Windows 10: the DNS Client service answers .local
// names itself and owns 5353, so a responder of our own binds without error and
// then answers nothing — the same failure macOS showed on the first install,
// and the reason the station could reach 127.0.0.1 but not bokkie.local.
//
// DnsServiceRegister (dnsapi.dll, Windows 10 1809 and later) is the Windows
// counterpart of dns-sd -P and Avahi: it hands the service record and the A
// record for the name to the responder that already owns the port. Called
// through syscalls, so no cgo.
func advertise(log *slog.Logger, hostname string, port int, addresses []string) (io.Closer, error) {
	address := primaryAddress(addresses)
	closer, err := registerWithWindows(hostname, port, address)
	if err == nil {
		log.Info("advertising over mDNS",
			"hostname", hostname+".local", "port", port, "address", address, "responder", "Windows DNS Client")
		return closer, nil
	}

	// No system responder to hand the name to — an older Windows. Ours is
	// then the only one on the machine, which is the case it was written for.
	log.Warn("Windows would not take the LAN name; answering for it ourselves", "error", err)
	return registerOwnResponder(log, hostname, port, addresses)
}

var (
	dnsapi                         = windows.NewLazySystemDLL("dnsapi.dll")
	procDnsServiceConstructInstace = dnsapi.NewProc("DnsServiceConstructInstance")
	procDnsServiceRegister         = dnsapi.NewProc("DnsServiceRegister")
	procDnsServiceDeRegister       = dnsapi.NewProc("DnsServiceDeRegister")
	procDnsServiceFreeInstance     = dnsapi.NewProc("DnsServiceFreeInstance")
)

const (
	dnsQueryRequestVersion1 = 1
	dnsRequestPending       = 9506 // DNS_REQUEST_PENDING: accepted, answer to follow
)

// dnsServiceRegisterRequest is DNS_SERVICE_REGISTER_REQUEST.
type dnsServiceRegisterRequest struct {
	Version        uint32
	InterfaceIndex uint32
	Instance       uintptr
	Callback       uintptr
	Context        uintptr
	Credentials    uintptr
	UnicastEnabled int32
}

// The completion callback is a C function pointer, and Go can make only a
// bounded number of those in a process — so there is one, and it reports to
// whichever registration is waiting through this channel.
var (
	completionOnce sync.Once
	completionCB   uintptr
	completions    = make(chan uint32, 4)
)

func onComplete(status uintptr, _ uintptr, instance uintptr) uintptr {
	// Windows hands back a copy of the instance, which is ours to free.
	if instance != 0 {
		procDnsServiceFreeInstance.Call(instance)
	}
	select {
	case completions <- uint32(status):
	default:
	}
	return 0
}

// windowsRegistration holds what Windows was handed. The request and the
// instance must stay alive until the name is withdrawn: Windows keeps
// pointers into both.
type windowsRegistration struct {
	request  *dnsServiceRegisterRequest
	instance uintptr
	once     sync.Once
}

// registrationWait is how long Windows gets to confirm the name. It probes the
// network for a conflict first, which takes a second or two.
const registrationWait = 10 * time.Second

func registerWithWindows(hostname string, port int, address string) (io.Closer, error) {
	if err := procDnsServiceRegister.Find(); err != nil {
		return nil, fmt.Errorf("this Windows has no DnsServiceRegister: %w", err)
	}
	ip := net.ParseIP(address).To4()
	if ip == nil {
		return nil, fmt.Errorf("%q is not an IPv4 address", address)
	}
	completionOnce.Do(func() { completionCB = syscall.NewCallback(onComplete) })

	service, err := syscall.UTF16PtrFromString(instanceName + "." + serviceType + ".local")
	if err != nil {
		return nil, err
	}
	host, err := syscall.UTF16PtrFromString(hostname + ".local")
	if err != nil {
		return nil, err
	}
	key, _ := syscall.UTF16PtrFromString("path")
	value, _ := syscall.UTF16PtrFromString("/")
	keys, values := []*uint16{key}, []*uint16{value}

	// IP4_ADDRESS is the address in network byte order, i.e. its four bytes
	// in the order they are written.
	ip4 := uint32(ip[0]) | uint32(ip[1])<<8 | uint32(ip[2])<<16 | uint32(ip[3])<<24

	instance, _, _ := procDnsServiceConstructInstace.Call(
		uintptr(unsafe.Pointer(service)),
		uintptr(unsafe.Pointer(host)),
		uintptr(unsafe.Pointer(&ip4)),
		0,
		uintptr(port), 0, 0,
		1,
		uintptr(unsafe.Pointer(&keys[0])),
		uintptr(unsafe.Pointer(&values[0])),
	)
	if instance == 0 {
		return nil, errors.New("Windows could not build the service record")
	}

	// Anything left from an earlier attempt would be taken for this one's
	// answer.
	for len(completions) > 0 {
		<-completions
	}

	reg := &windowsRegistration{
		instance: instance,
		request: &dnsServiceRegisterRequest{
			Version:  dnsQueryRequestVersion1,
			Instance: instance,
			Callback: completionCB,
		},
	}
	status, _, _ := procDnsServiceRegister.Call(uintptr(unsafe.Pointer(reg.request)), 0)
	if status != dnsRequestPending {
		procDnsServiceFreeInstance.Call(instance)
		return nil, fmt.Errorf("DnsServiceRegister refused: %w", syscall.Errno(status))
	}

	select {
	case result := <-completions:
		if result != 0 {
			reg.Close()
			return nil, fmt.Errorf("Windows would not publish %s.local: %w", hostname, syscall.Errno(result))
		}
	case <-time.After(registrationWait):
		// Not confirmed, not refused. The registration stands; the name check
		// a few seconds after start-up says whether it resolves.
	}
	return reg, nil
}

func (r *windowsRegistration) Close() error {
	r.once.Do(func() {
		procDnsServiceDeRegister.Call(uintptr(unsafe.Pointer(r.request)), 0)
		// The deregistration completes asynchronously too. Waiting briefly for
		// it keeps the instance alive while Windows may still be reading it.
		select {
		case <-completions:
		case <-time.After(2 * time.Second):
		}
		procDnsServiceFreeInstance.Call(r.instance)
	})
	return nil
}
