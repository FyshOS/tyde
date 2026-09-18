package status

import (
	"errors"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"fyshos.com/tyde"
	wmtheme "fyshos.com/tyde/theme"
	"github.com/FyshOS/networks/pkg/netman"
	"github.com/godbus/dbus/v5"
)

var networkMeta = tyde.ModuleMetadata{
	Name:        "Network",
	NewInstance: NewNetwork,
}

const networkNameEthernet = "Ethernet"

type network struct {
	name *widget.Label
	icon *widget.Button

	wasBlocked bool

	conn *dbus.Conn       // system bus for Wi-Fi browsing, opened lazily and reused
	net  *netman.Networks // iwd-backed network browser, built once on first use
}

func (n *network) Destroy() {
	if n.conn != nil {
		_ = n.conn.Close()
		n.conn, n.net = nil, nil
	}
}

func (n *network) wirelessName() (string, error) {
	net := ""
	iw, _ := exec.LookPath("iw")
	if iw == "" {
		iw, _ = exec.LookPath("/usr/sbin/iw")
	}
	if iw != "" {
		out, err := exec.Command("bash", []string{"-c", iw + " dev | grep ssid | cut -d ' ' -f2"}...).Output()
		if err != nil {
			log.Println("Error running iw", err)
			return "", err
		}
		net = strings.TrimSpace(string(out))
		if net == "" {
			return "", errors.New("no network connected")
		}
	} else {
		out, err := exec.Command("bash", []string{"-c", "/System/Library/PrivateFrameworks/Apple80211.framework/Resources/airport -I  | awk -F' SSID: '  '/ SSID: / {print $2}'"}...).Output()
		if err != nil {
			log.Println("Error getting network info from airport utility", err)
			return "", err
		}

		net = string(out)
	}
	return strings.TrimSpace(net), nil
}

// airportDevice returns the macOS device name of the Wi-Fi hardware port,
// or "" if this machine has none (as on CI runners and Mac minis without Wi-Fi).
func airportDevice() string {
	out, err := exec.Command("networksetup", "-listallhardwareports").Output()
	if err != nil {
		log.Println("Error running networksetup tool", err)
		return ""
	}

	return parseAirportDevice(string(out))
}

// parseAirportDevice picks the Wi-Fi device out of "networksetup -listallhardwareports" output.
func parseAirportDevice(out string) string {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "Hardware Port: Wi-Fi") && !strings.HasPrefix(line, "Hardware Port: AirPort") {
			continue
		}
		if i+1 >= len(lines) {
			break
		}
		return strings.TrimSpace(strings.TrimPrefix(lines[i+1], "Device:"))
	}

	return ""
}

func (n *network) isBlocked() (bool, error) {
	if ip, _ := exec.LookPath("rfkill"); ip != "" {
		out, err := exec.Command("bash", []string{"-c", "rfkill | grep \"wlan\""}...).Output()
		if err != nil {
			log.Println("Error running rfkill tool", err)
			return false, err
		}
		if strings.Contains(string(out), " blocked") {
			return true, nil
		}

		return false, nil
	}
	if ip, _ := exec.LookPath("networksetup"); ip != "" {
		dev := airportDevice()
		if dev == "" { // no Wi-Fi hardware, so nothing to block
			return false, nil
		}

		out, err := exec.Command("networksetup", "-getairportpower", dev).Output()
		if err != nil {
			log.Println("Error running networksetup tool", err)
			return false, err
		}
		// output looks like "Wi-Fi Power (en0): On"
		state := strings.TrimSpace(string(out))
		return strings.HasSuffix(state, ": Off"), nil
	}

	return false, nil
}

func (n *network) isEthernetConnected() (bool, error) {
	if ip, _ := exec.LookPath("ip"); ip != "" {
		out, err := exec.Command("bash", []string{"-c", "ip link | grep \",UP,\" | grep -v LOOPBACK | grep -v \": wl\" | wc -l"}...).Output()
		if err != nil {
			log.Println("Error running ip tool", err)
			return false, err
		}
		if strings.TrimSpace(string(out)) == "0" {
			return false, nil
		}
	} else if scutil, _ := exec.LookPath("scutil"); scutil != "" {
		out, err := exec.Command("bash", []string{"-c", "scutil --nwi | grep address | wc -l"}...).Output()
		if err != nil {
			log.Println("Error running scutil tool", err)
			return false, err
		}
		if strings.TrimSpace(string(out)) == "0" {
			return false, nil
		}
	} else {
		out, err := exec.Command("ifconfig").Output()
		if err != nil {
			log.Println("Error running ifconfig tool", err)
			return false, err
		}
		re, err := regexp.Compile(`^[^\t:]+:([^\n]|\n\t)*status: active`)
		if err != nil {
			log.Println("Error compiling regular expression", err)
			return false, err
		}
		m := re.FindSubmatch(out)
		if len(m) < 1 {
			return false, nil
		}
		// IPv4
		if strings.Contains(string(m[0]), "broadcast") {
			return true, nil
		}
		// IPv6, non-link-local only
		if found, _ := regexp.MatchString(`\s+inet6\s+[[:xdigit:]:]+\s+prefixlen\s+`, string(m[0])); found {
			return true, nil
		}
	}
	return true, nil
}

func (n *network) networkName() string {
	name, _ := n.wirelessName()
	if name != "" {
		return name
	}

	ether, _ := n.isEthernetConnected()
	if ether {
		return networkNameEthernet
	}
	return ""
}

func (n *network) tick() {
	tick := time.NewTicker(time.Second * 10)
	go func() {
		for {
			n.refreshContent()
			<-tick.C
		}
	}()
}

// refreshContent queries the network state and updates the status widget.
// Run on a background goroutine, and it will then refresh on fyne.Do.
func (n *network) refreshContent() {
	val := n.networkName()
	blocked, _ := n.isBlocked()

	fyne.Do(func() {
		if val != n.name.Text || blocked != n.wasBlocked {
			n.wasBlocked = blocked
			n.name.SetText(val)

			if blocked {
				n.icon.SetIcon(wmtheme.AirplaneIcon)
			} else if val == "" {
				n.icon.SetIcon(wmtheme.WifiOffIcon)
			} else if val == networkNameEthernet {
				n.icon.SetIcon(wmtheme.EthernetIcon)
			} else {
				n.icon.SetIcon(wmtheme.WifiIcon)
			}
		}
	})
}

func (n *network) StatusAreaWidget() fyne.CanvasObject {
	blocked := false
	if _, err := n.wirelessName(); err != nil {
		if blocked, err = n.isBlocked(); blocked || err != nil {
		}
		if _, err = n.isEthernetConnected(); err != nil && !blocked {
			return nil
		}
	}

	n.name = widget.NewLabel("")
	n.icon = &widget.Button{Icon: wmtheme.WifiOffIcon, Importance: widget.LowImportance, OnTapped: n.showMenu}
	if blocked {
		n.icon.Icon = wmtheme.AirplaneIcon
	}
	n.tick()

	return container.New(&handleNarrow{}, n.icon, n.name)
}

func (n *network) Metadata() tyde.ModuleMetadata {
	return networkMeta
}

func (n *network) setFlightMode(block bool) error {
	if ip, _ := exec.LookPath("rfkill"); ip != "" {
		out, err := exec.Command("bash", []string{"-c", "rfkill | grep \"wlan\""}...).Output()
		if err != nil {
			log.Println("Error running rfkill tool", err)
			return err
		}
		if len(out) < 3 {
			return errors.New("rfkill tool: rfkill output is too short")
		}

		id := strings.Split(strings.TrimSpace(string(out)), " ")[0]

		if id != "" {
			mode := "block"
			if !block {
				mode = "unblock"
			}
			cmd := exec.Command("bash", []string{"-c", "pkexec rfkill " + mode + " " + id}...)
			err = cmd.Start()
			if err != nil {
				log.Println("Error running rfkill tool", err)
				return err
			}

			go func() {
				cmd.Wait()
				n.refreshContent()
			}()
		}

		return nil
	}

	if ip, _ := exec.LookPath("networksetup"); ip != "" {
		dev := airportDevice()
		if dev == "" { // no Wi-Fi hardware, so nothing to toggle
			return nil
		}

		mode := "off"
		if !block {
			mode = "on"
		}
		err := exec.Command("networksetup", "-setairportpower", dev, mode).Run()
		if err != nil {
			log.Println("Error running networksetup tool", err)
			return err
		}

		n.refreshContent()
	}

	return nil
}

// showMenu pops up the network menu beneath the status icon: the Wi-Fi networks
// iwd currently knows about (from netman), followed by an Airplane Mode toggle.
func (n *network) showMenu() {
	// Avoid hanging with network calls.
	go func() {
		blocked, _ := n.isBlocked()

		var items []*fyne.MenuItem
		// The radio is off in airplane mode, so there are no networks to list.
		if !blocked {
			if nm := n.networks(); nm != nil {
				// Menu(nil) returns iwd's currently-known networks without blocking;
				// kick a background scan so the next open reflects any changes.
				items = append(items, nm.Menu(nil).Items...)
				go nm.Scan()
			}
		}
		if len(items) > 0 {
			items = append(items, fyne.NewMenuItemSeparator())
		}

		air := fyne.NewMenuItem("Airplane Mode", n.toggleFlightMode)
		air.Checked = blocked
		items = append(items, air)

		fyne.Do(func() {
			pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(n.icon)
			tyde.Instance().ShowMenuAt(fyne.NewMenu("", items...), pos)
		})
	}()
}

// networks lazily gets a network manager from our networks repo package that will generate our menu.
func (n *network) networks() *netman.Networks {
	if n.net != nil {
		return n.net
	}

	win := tyde.Instance().Root()
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		log.Println("network menu: system bus unavailable:", err)
		return nil
	}

	// handlePass prompts for a network passphrase, blocking until the user submits
	// or cancels; it is called from netman's iwd agent callback. Cancel returns "".
	handlePass := func(name string) string {
		result := make(chan string, 1)
		entry := widget.NewPasswordEntry()
		d := dialog.NewForm("Connect to "+name, "Connect", "Cancel",
			[]*widget.FormItem{widget.NewFormItem("Password", entry)},
			func(ok bool) {
				if ok {
					result <- entry.Text
				} else {
					result <- ""
				}
			}, win)
		fyne.Do(func() {
			entry.OnSubmitted = func(_ string) {
				d.Submit()
			}
			d.Resize(fyne.NewSize(320, d.MinSize().Height))
			d.Show()
		})

		return <-result
	}

	nm, err := netman.New(conn, handlePass, func(err error) {
		dialog.ShowError(err, win)
	})
	if err != nil {
		_ = conn.Close()
		log.Println("network menu: iwd unavailable:", err)
		return nil
	}
	n.conn, n.net = conn, nm
	return nm
}

func (n *network) toggleFlightMode() {
	// Avoid slow netowrk calls on graphical thread.
	go func() {
		blocked, err := n.isBlocked()
		if err != nil {
			fyne.LogError("blocking not supported", err)
			return
		}
		err = n.setFlightMode(!blocked)
		if err != nil {
			fyne.LogError("setting flight mode", err)
		}
	}()
}

// NewNetwork creates a new module that will show network information in the status area
func NewNetwork() tyde.Module {
	return &network{}
}
