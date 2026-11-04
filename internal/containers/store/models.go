// Package store owns public Connect IQ catalog browsing and watch decoration.
package store

import "github.com/DevWolk/garminctl/internal/ciqstore"

// View is a store page merged with what the watch has installed.
type View struct {
	Total int                      `json:"total"`
	Apps  []App                    `json:"apps"`
	Types []ciqstore.StoreTypeInfo `json:"types"`
	Sorts []ciqstore.StoreTypeInfo `json:"sorts"`
}

// App is a store app plus its state on the watch.
type App struct {
	ciqstore.StoreApp
	InstalledVersion int  `json:"installedVersion,omitempty"`
	Installed        bool `json:"installed"`
	UpdateAvailable  bool `json:"updateAvailable"`
}

// Detail is one app with reviews.
type Detail struct {
	App
	Reviews []ciqstore.StoreReview `json:"reviews"`
}
