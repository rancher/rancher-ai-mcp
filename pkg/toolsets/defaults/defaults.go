package defaults

import "os"

const rancherURL = "https://rancher.cattle-system.svc"

// RancherURL returns the value of the RANCHER_URL environment variable
// if set, otherwise it returns "https://rancher.cattle-system.svc".
func RancherURL() string {
	if v := os.Getenv("RANCHER_URL"); v != "" {
		return v
	}
	return rancherURL
}
