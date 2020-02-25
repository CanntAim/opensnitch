package firewall

import (
	"fmt"
	"sync"
	"time"
	"regexp"

	"github.com/gustavo-iniguez-goya/opensnitch/daemon/core"
)

const DropMark = 0x18BA5

type Action string

const (
	ADD     = Action("-A")
	INSERT  = Action("-I")
	DELETE  = Action("-D")
)

// make sure we don't mess with multiple rules
// at the same time
var (
	lock = sync.Mutex{}

	// check that rules are loaded every 5s
	rulesChecker = time.NewTicker(time.Second * 5)
	rulesCheckerChan = make(chan bool)
	regexRulesQuery, _ = regexp.Compile(`NFQUEUE.*ctstate NEW.*NFQUEUE num.*bypass`)
	regexDropQuery, _ = regexp.Compile(`DROP.*mark match 0x18ba5`)
)

func RunRule(action Action, enable bool, rule []string) (err error) {
	if enable == false {
		action = "-D"
	}

	rule = append([]string{string(action)}, rule...)

	lock.Lock()
	defer lock.Unlock()

	// fmt.Printf("iptables %s\n", rule)

	_, err = core.Exec("iptables", rule)
	if err != nil {
		return
	}
	_, err = core.Exec("ip6tables", rule)
	if err != nil {
		return
	}

	return
}

// INPUT --protocol udp --sport 53 -j NFQUEUE --queue-num 0 --queue-bypass
func QueueDNSResponses(enable bool, queueNum int) (err error) {
	return RunRule(INSERT, enable, []string{
		"INPUT",
		"--protocol", "udp",
		"--sport", "53",
		"-j", "NFQUEUE",
		"--queue-num", fmt.Sprintf("%d", queueNum),
		"--queue-bypass",
	})
}

// OUTPUT -t mangle -m conntrack --ctstate NEW -j NFQUEUE --queue-num 0 --queue-bypass
func QueueConnections(enable bool, queueNum int) (err error) {
	regexRulesQuery, _ = regexp.Compile(fmt.Sprint(`NFQUEUE.*ctstate NEW.*NFQUEUE num `, queueNum, ` bypass`))

	return RunRule(ADD, enable, []string{
		"OUTPUT",
		"-t", "mangle",
		"-m", "conntrack",
		"--ctstate", "NEW",
		"-j", "NFQUEUE",
		"--queue-num", fmt.Sprintf("%d", queueNum),
		"--queue-bypass",
	})
}

// Reject packets marked by OpenSnitch
// OUTPUT -m mark --mark 101285 -j DROP
func DropMarked(enable bool) (err error) {
	return RunRule(ADD, enable, []string{
		"OUTPUT",
		"-m", "mark",
		"--mark", fmt.Sprintf("%d", DropMark),
		"-j", "DROP",
	})
}

func AreRulesLoaded() bool {
	lock.Lock()
	defer lock.Unlock()

	outDrop, err := core.Exec("iptables", []string{"-L", "OUTPUT"})
	if err != nil {
		return false
	}
	outDrop6, err := core.Exec("ip6tables", []string{"-L", "OUTPUT"})
	if err != nil {
		return false
	}
	outMangle, err := core.Exec("iptables", []string{"-L", "OUTPUT", "-t", "mangle"})
	if err != nil {
		return false
	}
	outMangle6, err := core.Exec("ip6tables", []string{"-L", "OUTPUT", "-t", "mangle"})
	if err != nil {
		return false
	}

	return regexRulesQuery.FindString(outMangle) != "" &&
		regexRulesQuery.FindString(outMangle6) != "" &&
		regexDropQuery.FindString(outDrop) != "" &&
		regexDropQuery.FindString(outDrop6) != ""
}

func StartCheckingRules(qNum int) {
	for {
		select {
		case <-rulesCheckerChan:
			fmt.Println("Stop checking rules")
			return
		case <-rulesChecker.C:
			if rules := AreRulesLoaded(); rules == false {
				QueueConnections(false, qNum)
				DropMarked(false)
				QueueConnections(true, qNum)
				DropMarked(true)
			}
		}
	}
}

func StopCheckingRules() {
	rulesCheckerChan <- true
}
