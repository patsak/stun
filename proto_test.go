package stun

import (
	"net/netip"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarshal(t *testing.T) {
	expected := tmsg{
		tp:      msgTypeAck,
		addr:    netip.MustParseAddr("192.168.4.1"),
		payload: []byte{1, 0, 0, 1},
	}

	bts, err := expected.MarshalBinary()
	require.NoError(t, err)

	var actual tmsg
	require.NoError(t, actual.UnmarshalBinary(bts))

	require.Equal(t, expected, actual)
}
