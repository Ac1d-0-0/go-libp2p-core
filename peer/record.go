package peer

import (
	"errors"
	"fmt"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/libp2p/go-libp2p-core/crypto"
	pb "github.com/libp2p/go-libp2p-core/peer/pb"
	"github.com/libp2p/go-libp2p-core/record"
	ma "github.com/multiformats/go-multiaddr"
)

func init() {
	record.RegisterPayloadType(PeerRecordEnvelopePayloadType, &PeerRecord{})
}

// The domain string used for peer records contained in a Envelope.
const PeerRecordEnvelopeDomain = "libp2p-peer-record"

// The type hint used to identify peer records in a Envelope.
// TODO: register multicodec
var PeerRecordEnvelopePayloadType = []byte("/libp2p/peer-record")

// ErrPeerIdMismatch is returned when attempting to sign a PeerRecord using a key that
// does not match the PeerID contained in the record.
var ErrPeerIdMismatch = errors.New("signing key does not match record.PeerID")

// PeerRecord contains information that is broadly useful to share with other peers,
// either through a direct exchange (as in the libp2p identify protocol), or through
// a Peer Routing provider, such as a DHT.
//
// Currently, a PeerRecord contains the public listen addresses for a peer, but this
// is expected to expand to include other information in the future.
//
// PeerRecords are ordered in time by their Seq field. Newer PeerRecords must have
// greater Seq values than older records. The NewPeerRecord function will create
// a PeerRecord with a timestamp-based Seq value. The other PeerRecord fields should
// be set by the caller:
//
//    rec := peer.NewPeerRecord()
//    rec.PeerID = aPeerID
//    rec.Addrs = someAddrs
//
// Alternatively, you can construct a PeerRecord struct directly and use the TimestampSeq
// helper to set the Seq field:
//
//    rec := peer.PeerRecord{PeerID: aPeerID, Addrs: someAddrs, Seq: peer.TimestampSeq()}
//
// Failing to set the Seq field will not result in an error, however, a PeerRecord with a
// Seq value of zero may be ignored or rejected by other peers.
//
// PeerRecords are intended to be shared with other peers inside a signed
// routing.Envelope, and PeerRecord implements the routing.Record interface
// to facilitate this.
//
// To share a PeerRecord, first call Sign to wrap the record in a Envelope
// and sign it with the local peer's private key:
//
//     rec := &PeerRecord{PeerID: myPeerId, Addrs: myAddrs}
//     envelope, err := rec.Sign(myPrivateKey)
//
// The resulting record.Envelope can be marshalled to a []byte and shared
// publicly. As a convenience, the MarshalSigned method will produce the
// Envelope and marshal it to a []byte in one go:
//
//     rec := &PeerRecord{PeerID: myPeerId, Addrs: myAddrs}
//     recordBytes, err := rec.MarshalSigned(myPrivateKey)
//
// To validate and unmarshal a signed PeerRecord from a remote peer,
// "consume" the containing envelope, which will return both the
// routing.Envelope and the inner Record. The Record must be cast to
// a PeerRecord pointer before use:
//
//     envelope, untypedRecord, err := ConsumeEnvelope(envelopeBytes, PeerRecordEnvelopeDomain)
//     if err != nil {
//       handleError(err)
//       return
//     }
//     peerRec := untypedRecord.(*PeerRecord)
//
type PeerRecord struct {
	// PeerID is the ID of the peer this record pertains to.
	PeerID ID

	// Addrs contains the public addresses of the peer this record pertains to.
	Addrs []ma.Multiaddr

	// Seq is a monotonically-increasing sequence counter that's used to order
	// PeerRecords in time. The interval between Seq values is unspecified,
	// but newer PeerRecords MUST have a greater Seq value than older records
	// for the same peer.
	Seq uint64
}

// NewPeerRecord returns a PeerRecord with a timestamp-based sequence number.
// The returned record is otherwise empty and should be populated by the caller.
func NewPeerRecord() *PeerRecord {
	return &PeerRecord{Seq: TimestampSeq()}
}

// PeerRecordFromAddrInfo creates a PeerRecord from an AddrInfo struct.
// The returned record will have a timestamp-based sequence number.
func PeerRecordFromAddrInfo(info AddrInfo) *PeerRecord {
	rec := NewPeerRecord()
	rec.PeerID = info.ID
	rec.Addrs = info.Addrs
	return rec
}

// TimestampSeq is a helper to generate a timestamp-based sequence number for a PeerRecord.
func TimestampSeq() uint64 {
	return uint64(time.Now().UnixNano())
}

// UnmarshalRecord parses a PeerRecord from a byte slice.
// This method is called automatically when consuming a record.Envelope
// whose PayloadType indicates that it contains a PeerRecord.
// It is generally not necessary or recommended to call this method directly.
func (r *PeerRecord) UnmarshalRecord(bytes []byte) error {
	if r == nil {
		return fmt.Errorf("cannot unmarshal PeerRecord to nil receiver")
	}

	var msg pb.PeerRecord
	err := proto.Unmarshal(bytes, &msg)
	if err != nil {
		return err
	}
	var id ID
	err = id.UnmarshalBinary(msg.PeerId)
	if err != nil {
		return err
	}
	r.PeerID = id
	r.Addrs = addrsFromProtobuf(msg.Addresses)
	r.Seq = msg.Seq
	return nil
}

// MarshalRecord serializes a PeerRecord to a byte slice.
// This method is called automatically when constructing a routing.Envelope
// using MakeEnvelopeWithRecord or PeerRecord.Sign.
func (r *PeerRecord) MarshalRecord() ([]byte, error) {
	idBytes, err := r.PeerID.MarshalBinary()
	if err != nil {
		return nil, err
	}
	msg := pb.PeerRecord{
		PeerId:    idBytes,
		Addresses: addrsToProtobuf(r.Addrs),
		Seq:       r.Seq,
	}
	return proto.Marshal(&msg)
}

// Sign wraps the PeerRecord in a routing.Envelope, signed with the given
// private key. The private key must match the PeerID field of the PeerRecord.
func (r *PeerRecord) Sign(privKey crypto.PrivKey) (*record.Envelope, error) {
	p, err := IDFromPrivateKey(privKey)
	if err != nil {
		return nil, err
	}
	if p != r.PeerID {
		return nil, ErrPeerIdMismatch
	}
	return record.MakeEnvelopeWithRecord(privKey, PeerRecordEnvelopeDomain, PeerRecordEnvelopePayloadType, r)
}

// MarshalSigned is a convenience method that wraps the PeerRecord in a routing.Envelope,
// and marshals the Envelope to a []byte.
func (r *PeerRecord) MarshalSigned(privKey crypto.PrivKey) ([]byte, error) {
	env, err := r.Sign(privKey)
	if err != nil {
		return nil, err
	}
	return env.Marshal()
}

// Equal returns true if the other PeerRecord is identical to this one.
func (r *PeerRecord) Equal(other *PeerRecord) bool {
	if other == nil {
		return r == nil
	}
	if r.PeerID != other.PeerID {
		return false
	}
	if r.Seq != other.Seq {
		return false
	}
	if len(r.Addrs) != len(other.Addrs) {
		return false
	}
	for i, _ := range r.Addrs {
		if !r.Addrs[i].Equal(other.Addrs[i]) {
			return false
		}
	}
	return true
}

func addrsFromProtobuf(addrs []*pb.PeerRecord_AddressInfo) []ma.Multiaddr {
	var out []ma.Multiaddr
	for _, addr := range addrs {
		a, err := ma.NewMultiaddrBytes(addr.Multiaddr)
		if err != nil {
			continue
		}
		out = append(out, a)
	}
	return out
}

func addrsToProtobuf(addrs []ma.Multiaddr) []*pb.PeerRecord_AddressInfo {
	var out []*pb.PeerRecord_AddressInfo
	for _, addr := range addrs {
		out = append(out, &pb.PeerRecord_AddressInfo{Multiaddr: addr.Bytes()})
	}
	return out
}
