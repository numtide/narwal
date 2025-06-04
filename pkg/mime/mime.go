package mime

import "github.com/numtide/narwal/pkg/db"

const (
	Debug      = "application/json"
	Nar        = "application/x-nix-nar"
	NarInfo    = "text/x-nix-narinfo"
	NarListing = "text/x-nix-ls"
)

//nolint:gochecknoglobals
var mimeTypes = map[db.ObjectType]string{
	db.ObjectTypeDebug:   Debug,
	db.ObjectTypeLs:      NarListing,
	db.ObjectTypeNar:     Nar,
	db.ObjectTypeNarinfo: NarInfo,
}

func For(objectType db.ObjectType) string {
	return mimeTypes[objectType]
}
