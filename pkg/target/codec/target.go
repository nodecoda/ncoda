package codec

import "github.com/nodecoda/ncoda/pkg/types"

// TargetName is the sole Dify target profile supported by this port,
// mirroring dify_type_codec.py's DIFY_116_GRAPHON_06_TYPE_CODEC profile name.
const TargetName = "dify-1.16-graphon-0.6"

// Default returns the codec locked to TargetName, mirroring the module-level
// DIFY_116_GRAPHON_06_TYPE_CODEC constant.
func Default() *Codec { return &Codec{ProfileName: TargetName} }

// Interface is the physical-type mapping face TargetProfile carries (I3):
// the parameter-extractor codec target validation consults for EXACT-loss
// extraction-field certification. Defined in the shared internal/target
// layer so the passes frontend can consume the face without importing a
// backend package; *Codec satisfies it structurally. A second target plugs
// in here when its profile codec lands.
type Interface interface {
	ToParameterExtractorField(d *types.Descriptor) *Shape
}

var _ Interface = (*Codec)(nil)
