package web

import (
	"errors"
	"net/http"
	"slices"
)

// connectionModes: an Access port carries one untagged VLAN; a Trunk carries
// tagged VLANs (and optionally a native/untagged one).
var connectionModes = []string{"Access", "Trunk"}

// validateConnection enforces the cross-field rules the generic form parser
// can't express. Messages are capitalized for display by friendlyErr.
func validateConnection(r *http.Request) error {
	dev, sw := r.PostFormValue("device_id"), r.PostFormValue("switch_id")
	if dev != "" && dev == sw {
		return errors.New("a device cannot connect to itself")
	}
	untagged := r.PostFormValue("untagged_vlan_id")
	tagged := r.PostForm["tagged_vlans"]
	switch r.PostFormValue("mode") {
	case "Access":
		if untagged == "" {
			return errors.New("an access port needs its VLAN: choose the untagged VLAN")
		}
		if len(tagged) > 0 {
			return errors.New("an access port carries a single VLAN; use Trunk to carry several")
		}
	case "Trunk":
		if len(tagged) == 0 {
			return errors.New("a trunk needs at least one tagged VLAN")
		}
		if untagged != "" && slices.Contains(tagged, untagged) {
			return errors.New("the native (untagged) VLAN cannot also be tagged")
		}
	}
	return nil
}
