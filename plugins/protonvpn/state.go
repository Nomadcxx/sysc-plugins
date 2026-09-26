package protonvpn

import "time"

const transitionDeadline = 20 * time.Second

// Snapshot is an immutable view of the plugin state, returned by value.
type Snapshot struct {
	Phase  Phase
	Err    string // error detail line; cleared on the next state change
	Status Status
	Info   Info
	Config Config
	IP     string // from connect stdout; cleared on disconnect

	Interface string // tunnel device from the link watch; "" when down

	RxRate, TxRate   float64 // bytes/s
	RxTotal, TxTotal float64

	Port int // NAT-PMP forwarded port; 0 = none
}

// Machine holds the plugin state and applies poll results to it.
type Machine struct {
	Now func() time.Time // injectable; defaults to time.Now when nil

	snap              Snapshot
	transitionStarted time.Time
	lastRx, lastTx    uint64
	lastSample        time.Time
}

func (m *Machine) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}
	return m.Now()
}

func (m *Machine) Snapshot() Snapshot { return m.snap }

func (m *Machine) SetStatus(s Status) {
	changed := m.snap.Phase != s.Phase
	if changed {
		m.snap.Err = ""
	}
	if s.Phase == PhaseDisconnected {
		m.snap.IP = ""
	}
	m.snap.Status = s
	m.snap.Phase = s.Phase
	switch s.Phase {
	case PhaseConnected, PhaseDisconnected:
		m.transitionStarted = time.Time{}
	case PhaseConnecting, PhaseDisconnecting:
		if changed {
			m.transitionStarted = m.now()
		}
	}
}

func (m *Machine) SetInfo(i Info)           { m.snap.Info = i }
func (m *Machine) SetConfig(c Config)       { m.snap.Config = c }
func (m *Machine) SetIP(ip string)          { m.snap.IP = ip }
func (m *Machine) SetInterface(name string) { m.snap.Interface = name }
func (m *Machine) SetPort(p int)            { m.snap.Port = p }

func (m *Machine) StartConnect() {
	m.snap.Phase = PhaseConnecting
	m.snap.Err = ""
	m.transitionStarted = m.now()
}

func (m *Machine) StartDisconnect() {
	m.snap.Phase = PhaseDisconnecting
	m.snap.Err = ""
	m.snap.IP = ""
	m.transitionStarted = m.now()
}

func (m *Machine) Fail(detail string) {
	m.snap.Phase = PhaseError
	m.snap.Err = detail
}

func (m *Machine) SampleTraffic(rx, tx uint64, ifaceExists bool) {
	if !ifaceExists {
		m.snap.RxRate = 0
		m.snap.TxRate = 0
		// restart the rate baseline so reappearance with reset counters doesn't underflow
		m.lastRx, m.lastTx = 0, 0
		m.lastSample = time.Time{}
		return
	}
	now := m.now()
	if !m.lastSample.IsZero() {
		if elapsed := now.Sub(m.lastSample).Seconds(); elapsed > 0 {
			m.snap.RxRate = float64(rx-m.lastRx) / elapsed
			m.snap.TxRate = float64(tx-m.lastTx) / elapsed
		}
	}
	m.snap.RxTotal = float64(rx)
	m.snap.TxTotal = float64(tx)
	m.lastRx, m.lastTx = rx, tx
	m.lastSample = now
}

func (m *Machine) TransitionExpired() bool {
	if m.snap.Phase != PhaseConnecting && m.snap.Phase != PhaseDisconnecting {
		return false
	}
	return m.now().Sub(m.transitionStarted) >= transitionDeadline
}
