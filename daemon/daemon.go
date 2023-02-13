package daemon

import (
	"fmt"
	"log"
	"syscall"
	"strings"
	"os"
	"io/ioutil"
	"encoding/csv"
	"path/filepath"

	"github.com/pmorjan/kmod"
	"github.com/mdlayher/kobject"
	wildcard "github.com/IGLOU-EU/go-wildcard"
//	"github.com/davecgh/go-spew/spew"
)

func Start() {
	fmt.Println("Puredev v1.0 started.")

	// Kernel release name
	var un syscall.Utsname
	err := syscall.Uname(&un)
	if err != nil {
	        log.Fatal(err)
	}
	var buf [65]byte
	var release string
	for i, b := range un.Release[:] {
		buf[i] = uint8(b)
		if b == 0 {
			release = string(buf[:i])
			break
		}
	}

	// Kernel module lookup aliases
	modules := "/lib/modules/" + release + "/modules.alias"
	fd, err := os.Open(modules)
	if err != nil {
		log.Fatal("Unable to read file: " + modules, err)
	}
	defer fd.Close()

	csvReader := csv.NewReader(fd)
	csvReader.Comma = ' '
	csvReader.Comment = '#'
	csvReader.FieldsPerRecord = -1
	records, err := csvReader.ReadAll()
	if err != nil {
		log.Fatal("Unable to parse module.alias file", err)
	}

	aliases := make([][]string, len(records))
	for row, value := range records {
		aliases[row] = []string{value[1], value[2]}
	}

	// Netlink connection for uevents
	conn, err := kobject.New()
	if err != nil {
	        log.Fatal(err)
	}
	defer conn.Close()

	fmt.Println("Coldplug device list:")

	// Which cold devices should we trigger hotplug events for?
	coldplugFilter := []string{
		"OF_NAME=wifi",         // WIFI on SDIO
		"PRODUCT=fe6/9700/101", // USB Ethernet
		"DEVNAME=sda1",         // USB Storage
	}
	for i, filter_line := range coldplugFilter {
		fmt.Printf(" %d. %s\n",i,filter_line)
	}

	fmt.Println("Manually trigger hotplug events for selected cold-plug devices.")

	go func() {
		// Make coldplugged devices generate hotplug events so we deal
		// with both cold and hot devices exactly the same way
		// We only pick WIFI on SDIO here, otherwise we load all possible modules
		err = filepath.Walk("/sys", func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.Name() == "uevent" {
				// Read the uevent details
				data, err := os.ReadFile(path)
				if err == nil {
					// We do not handle permission properly. Some uevent files
					// is not accessible and has no read permissions. We just
					// skip those for now.
					uevent_lines := strings.Split(string(data),"\n")
					found := false
					for _, filter_line := range coldplugFilter {
						for _, uevent_line := range uevent_lines {
							if strings.TrimSpace(filter_line) == strings.TrimSpace(uevent_line) {
								found = true
								break
							}
						}
					}

					if found {
						// Generate a hotplug add event
						os.WriteFile(path, []byte("add"), 0)
					}
				}
			}
			return nil
			})

		if err != nil {
			log.Println(err)
		}
	}()

	fmt.Println("Listening for device hotplug events:")
	fmt.Println()

	for {
		event, err := conn.Receive()
		if err != nil {
			log.Fatal(err)
		}

//		fmt.Printf("Event: %s, Subsystem: %s, DevicePath: %s, Sequence: %d\n", event.Action, event.Subsystem, event.DevicePath, event.Sequence)

		if event.Action == "add" {

			// Module loads
			modalias := "/sys" + event.DevicePath + "/modalias"
			if _, err := os.Stat(modalias); err == nil {

				bytes, err := ioutil.ReadFile(modalias)
				if err != nil {
					log.Fatal("Unable to read file: " + modalias, err)
				}
				alias := strings.TrimSpace(string(bytes))
				if alias != "" {

					// Match logic which leads to module loading and other actions
					found := false
					for _, wild_alias := range aliases {
						if wildcard.Match(wild_alias[0], alias) {
							if !found {
								fmt.Printf("Finding modules for modalias %v ...\n", alias)
							}

							found = true
							fmt.Printf("Loading module %v ...\n", wild_alias[1])
							// Call kmod
							k, err := kmod.New()
							if err != nil {
								log.Fatal(err)
							}
							if err := k.Load(wild_alias[1], "", 0); err != nil {
								log.Fatal(err)
							}
						}
					}
				}
			}

			// Device Rules
			if event.Subsystem == "block" {
				partition := "/sys" + event.DevicePath + "/partition"
				if _, err := os.Stat(partition); err == nil {
					blks := strings.Split(event.DevicePath,"/")
					fmt.Printf("Rule found: print storage partition device node: %v ...\n", "/dev/" + blks[len(blks)-1] )
				}
			}
		}
	}
}
