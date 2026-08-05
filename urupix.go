// urupix.go
//
// Custom logic for Urupix extensions.

package main

import "go.mau.fi/whatsmeow/types"

// filterSavedContacts returns only contacts that have a saved name in the phone's address book.
func filterSavedContacts(
	contacts map[types.JID]types.ContactInfo,
) map[types.JID]types.ContactInfo {
	filtered := make(map[types.JID]types.ContactInfo)

	for jid, info := range contacts {
		if info.FullName != "" ||
			info.FirstName != "" ||
			info.BusinessName != "" {
			filtered[jid] = info
		}
	}

	return filtered
}