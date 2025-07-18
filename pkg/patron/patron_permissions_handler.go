package patron

import "net/http"

// PermissionHandler is the interface for handling updates to a users permissions.
type PermissionHandler interface {

	// HandlePermissions handles the request to update a users permissions.
	HandlePermissions(w http.ResponseWriter, r *http.Request) 
}