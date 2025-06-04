package mime

import "github.com/numtide/narwal/pkg/db"

const (
	Nar        = "application/x-nix-nar"
	Drv        = "application/x-nix-drv"
	NarInfo    = "text/x-nix-narinfo"
	NarListing = "text/x-nix-ls"
)

//nolint:gochecknoglobals
var mimeTypes = map[db.ObjectType]string{
	db.ObjectTypeNar:     Nar,
	db.ObjectTypeNarinfo: NarInfo,
	db.ObjectTypeDrv:     Drv,
	db.ObjectTypeLs:      NarListing,
}

func For(objectType db.ObjectType) string {
	return mimeTypes[objectType]
}
