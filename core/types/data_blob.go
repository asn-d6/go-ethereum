package types

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/crypto/kzg"
	"github.com/ethereum/go-ethereum/params"
	"github.com/protolambda/go-kzg/bls"
	"github.com/protolambda/ztyp/codec"
	"github.com/protolambda/ztyp/tree"
)

const BLOB_COMMITMENT_VERSION_KZG byte = 0x01
const MAX_TX_WRAP_KZG_COMMITMENTS = 1 << 24
const LIMIT_BLOBS_PER_TX = 1 << 24

// Compressed BLS12-381 G1 element
type KZGCommitment [48]byte

func (p *KZGCommitment) Deserialize(dr *codec.DecodingReader) error {
	if p == nil {
		return errors.New("nil pubkey")
	}
	_, err := dr.Read(p[:])
	return err
}

func (p *KZGCommitment) Serialize(w *codec.EncodingWriter) error {
	return w.Write(p[:])
}

func (KZGCommitment) ByteLength() uint64 {
	return 48
}

func (KZGCommitment) FixedLength() uint64 {
	return 48
}

func (p KZGCommitment) HashTreeRoot(hFn tree.HashFn) tree.Root {
	var a, b tree.Root
	copy(a[:], p[0:32])
	copy(b[:], p[32:48])
	return hFn(a, b)
}

func (p KZGCommitment) MarshalText() ([]byte, error) {
	return []byte("0x" + hex.EncodeToString(p[:])), nil
}

func (p KZGCommitment) String() string {
	return "0x" + hex.EncodeToString(p[:])
}

func (p *KZGCommitment) UnmarshalText(text []byte) error {
	if p == nil {
		return errors.New("cannot decode into nil KZGCommitment")
	}
	if len(text) >= 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X') {
		text = text[2:]
	}
	if len(text) != 96 {
		return fmt.Errorf("unexpected length string '%s'", string(text))
	}
	_, err := hex.Decode(p[:], text)
	return err
}

func (p *KZGCommitment) Point() (*bls.G1Point, error) {
	return bls.FromCompressedG1(p[:])
}

func (kzg KZGCommitment) ComputeVersionedHash() common.Hash {
	h := crypto.Keccak256Hash(kzg[:])
	h[0] = BLOB_COMMITMENT_VERSION_KZG
	return h
}

type BLSFieldElement [32]byte

func ReadFieldElements(dr *codec.DecodingReader, elems *[]BLSFieldElement, length uint64) error {
	if uint64(len(*elems)) != length {
		// re-use space if available (for recycling old state objects)
		if uint64(cap(*elems)) >= length {
			*elems = (*elems)[:length]
		} else {
			*elems = make([]BLSFieldElement, length, length)
		}
	}
	dst := *elems
	for i := uint64(0); i < length; i++ {
		// TODO: do we want to check if each field element is within range?
		if _, err := dr.Read(dst[i][:]); err != nil {
			return err
		}
	}
	return nil
}

func WriteFieldElements(ew *codec.EncodingWriter, elems []BLSFieldElement) error {
	for i := range elems {
		if err := ew.Write(elems[i][:]); err != nil {
			return err
		}
	}
	return nil
}

// Blob data
type Blob []BLSFieldElement

func (blob *Blob) Deserialize(dr *codec.DecodingReader) error {
	return ReadFieldElements(dr, (*[]BLSFieldElement)(blob), params.FieldElementsPerBlob)
}

func (blob Blob) Serialize(w *codec.EncodingWriter) error {
	return WriteFieldElements(w, blob)
}

func (blob Blob) ByteLength() (out uint64) {
	return params.FieldElementsPerBlob * 32
}

func (blob *Blob) FixedLength() uint64 {
	return params.FieldElementsPerBlob * 32
}

func (blob Blob) HashTreeRoot(hFn tree.HashFn) tree.Root {
	return hFn.ComplexVectorHTR(func(i uint64) tree.HTR {
		return (*tree.Root)(&blob[i])
	}, params.FieldElementsPerBlob)
}

func (blob Blob) copy() Blob {
	cpy := make(Blob, len(blob))
	copy(cpy, blob)
	return cpy
}

func (blob Blob) ComputeCommitment() (commitment KZGCommitment, ok bool) {
	frs := make([]bls.Fr, len(blob))
	for i, elem := range blob {
		if !bls.FrFrom32(&frs[i], elem) {
			return KZGCommitment{}, false
		}
	}
	// data is presented in eval form
	commitmentG1 := kzg.BlobToKzg(frs)
	var out KZGCommitment
	copy(out[:], bls.ToCompressedG1(commitmentG1))
	return out, true
}

type BlobKzgs []KZGCommitment

// Extract the crypto material underlying these commitments
func (li BlobKzgs) Commitments() ([]*bls.G1Point, error) {
	var points []*bls.G1Point
	for _, c := range li {
		p, err := c.Point()
		if err != nil {
			return nil, errors.New("internal error commitments")
		}
		points = append(points, p)
	}
	return points, nil
}

func (li *BlobKzgs) Deserialize(dr *codec.DecodingReader) error {
	return dr.List(func() codec.Deserializable {
		i := len(*li)
		*li = append(*li, KZGCommitment{})
		return &(*li)[i]
	}, 48, MAX_TX_WRAP_KZG_COMMITMENTS)
}

func (li BlobKzgs) Serialize(w *codec.EncodingWriter) error {
	return w.List(func(i uint64) codec.Serializable {
		return &li[i]
	}, 48, uint64(len(li)))
}

func (li BlobKzgs) ByteLength() uint64 {
	return uint64(len(li)) * 48
}

func (li *BlobKzgs) FixedLength() uint64 {
	return 0
}

func (li BlobKzgs) HashTreeRoot(hFn tree.HashFn) tree.Root {
	return hFn.ComplexListHTR(func(i uint64) tree.HTR {
		return &li[i]
	}, uint64(len(li)), MAX_TX_WRAP_KZG_COMMITMENTS)
}

func (li BlobKzgs) copy() BlobKzgs {
	cpy := make(BlobKzgs, len(li))
	copy(cpy, li)
	return cpy
}

type Blobs []Blob

// Extract the crypto material underlying these blobs
func (blobs Blobs) Blobs() ([][]bls.Fr, error) {
	var result [][]bls.Fr

	// Iterate over every blob
	for _, b := range blobs {
		var blob []bls.Fr
		// Iterate over each chunk of the blob and parse it into an Fr
		for _, chunk := range b {
			var chunkFr bls.Fr
			ok := bls.FrFrom32(&chunkFr, chunk)
			if ok != true {
				return nil, errors.New("internal error commitments")
			}
			blob = append(blob, chunkFr)
		}
		// Add each individiual blob to the result
		result = append(result, blob)
	}
	return result, nil
}

func (a *Blobs) Deserialize(dr *codec.DecodingReader) error {
	return dr.List(func() codec.Deserializable {
		i := len(*a)
		*a = append(*a, Blob{})
		return &(*a)[i]
	}, params.FieldElementsPerBlob*32, LIMIT_BLOBS_PER_TX)
}

func (a Blobs) Serialize(w *codec.EncodingWriter) error {
	return w.List(func(i uint64) codec.Serializable {
		return &a[i]
	}, params.FieldElementsPerBlob*32, uint64(len(a)))
}

func (a Blobs) ByteLength() (out uint64) {
	return uint64(len(a)) * params.FieldElementsPerBlob * 32
}

func (a *Blobs) FixedLength() uint64 {
	return 0 // it's a list, no fixed length
}

func (li Blobs) HashTreeRoot(hFn tree.HashFn) tree.Root {
	length := uint64(len(li))
	return hFn.ComplexListHTR(func(i uint64) tree.HTR {
		if i < length {
			return &li[i]
		}
		return nil
	}, length, LIMIT_BLOBS_PER_TX)
}

func (blobs Blobs) copy() Blobs {
	cpy := make(Blobs, len(blobs))
	for i, bl := range blobs {
		cpy[i] = bl.copy()
	}
	return cpy
}

type BlobTxWrapper struct {
	Tx       SignedBlobTx
	BlobKzgs BlobKzgs
	Blobs    Blobs
}

func (txw *BlobTxWrapper) Deserialize(dr *codec.DecodingReader) error {
	return dr.Container(&txw.Tx, &txw.BlobKzgs, &txw.Blobs)
}

func (txw *BlobTxWrapper) Serialize(w *codec.EncodingWriter) error {
	return w.Container(&txw.Tx, &txw.BlobKzgs, &txw.Blobs)
}

func (txw *BlobTxWrapper) ByteLength() uint64 {
	return codec.ContainerLength(&txw.Tx, &txw.BlobKzgs, &txw.Blobs)
}

func (txw *BlobTxWrapper) FixedLength() uint64 {
	return 0
}

func (txw *BlobTxWrapper) HashTreeRoot(hFn tree.HashFn) tree.Root {
	return hFn.HashTreeRoot(&txw.Tx, &txw.BlobKzgs, &txw.Blobs)
}

type BlobTxWrapData struct {
	BlobKzgs BlobKzgs
	Blobs    Blobs
}

func (b *BlobTxWrapData) sizeWrapData() common.StorageSize {
	return common.StorageSize(4 + 4 + b.BlobKzgs.ByteLength() + b.Blobs.ByteLength())
}

func (b *BlobTxWrapData) checkWrapping(inner TxData) error {
	blobTx, ok := inner.(*SignedBlobTx)
	if !ok {
		return fmt.Errorf("expected signed blob tx, got %T", inner)
	}
	if a, b := len(blobTx.Message.BlobVersionedHashes), params.MaxBlobsPerTx; a > b {
		return fmt.Errorf("too many blobs in blob tx, got %d, expected no more than %d", a, b)
	}
	if a, b := len(b.BlobKzgs), len(b.Blobs); a != b {
		return fmt.Errorf("expected equal amount but got %d kzgs and %d blobs", a, b)
	}
	if a, b := len(b.BlobKzgs), len(blobTx.Message.BlobVersionedHashes); a != b {
		return fmt.Errorf("expected equal amount but got %d kzgs and %d versioned hashes", a, b)
	}
	for i, h := range blobTx.Message.BlobVersionedHashes {
		if computed := b.BlobKzgs[i].ComputeVersionedHash(); computed != h {
			return fmt.Errorf("versioned hash %d supposedly %s but does not match computed %s", i, h, computed)
		}
	}

	// Extract cryptographic material out of our types and pass them to the crypto layer
	commitments, err := b.BlobKzgs.Commitments()
	if err != nil {
		return fmt.Errorf("internal commitment error")
	}
	blobs, err := b.Blobs.Blobs()
	if err != nil {
		return fmt.Errorf("internal blobs error")
	}
	return kzg.VerifyBlobs(commitments, blobs)
}

func (b *BlobTxWrapData) copy() TxWrapData {
	return &BlobTxWrapData{
		BlobKzgs: b.BlobKzgs.copy(),
		Blobs:    b.Blobs.copy(),
	}
}

func (b *BlobTxWrapData) kzgs() BlobKzgs {
	return b.BlobKzgs
}

func (b *BlobTxWrapData) blobs() Blobs {
	return b.Blobs
}

func (b *BlobTxWrapData) encodeTyped(w io.Writer, txdata TxData) error {
	if _, err := w.Write([]byte{BlobTxType}); err != nil {
		return err
	}
	blobTx, ok := txdata.(*SignedBlobTx)
	if !ok {
		return fmt.Errorf("expected signed blob tx, got %T", txdata)
	}
	wrapped := BlobTxWrapper{
		Tx:       *blobTx,
		BlobKzgs: b.BlobKzgs,
		Blobs:    b.Blobs,
	}
	return EncodeSSZ(w, &wrapped)
}
