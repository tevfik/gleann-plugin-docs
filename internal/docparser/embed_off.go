//go:build !embedmodels

package docparser

// embeddedModel reports no embedded models: the default build keeps the
// binary small and loads the ONNX files from the model directory.
func embeddedModel(string) ([]byte, bool) { return nil, false }
