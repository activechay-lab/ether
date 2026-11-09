package listcache

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// renewBatch bounds one RenewPage call.
const renewBatch = 4096

var errCorrupt = errors.New("listing cache: corrupt image record")

// KeepImages records one page's signed artwork beside the listing, so its
// image URLs can be re-armed for another credential, or once the grants
// SignPage issued expire. Pages may be signed concurrently.
func (e *Entry) KeepImages(items []frontend.ImageBatchItem) error {
	e.keepMu.Lock()
	defer e.keepMu.Unlock()
	var buf [binary.MaxVarintLen64]byte
	var err error
	put := func(v uint64) {
		if err == nil {
			_, err = e.images.Write(buf[:binary.PutUvarint(buf[:], v)])
		}
	}
	for _, item := range items {
		if item.Item <= 0 || len(item.Images) == 0 {
			continue
		}
		put(uint64(item.Item))
		put(uint64(len(item.Images)))
		for _, g := range item.Images {
			for _, v := range []string{g.Secret, g.Image.Kind, g.Image.Path, g.Image.Tag, g.Image.Subject} {
				put(uint64(len(v)))
				if err == nil {
					_, err = e.images.WriteString(v)
				}
			}
		}
	}
	if err != nil {
		return &diskError{err}
	}
	return nil
}

// RenewImages re-arms e's artwork for credential and device.
func RenewImages(ctx context.Context, e *Entry, renewer frontend.ImagePageRenewer, credential string, device frontend.Device) error {
	return e.readImages(func(items []frontend.ImageBatchItem) error {
		return renewer.RenewPage(ctx, frontend.ImagePageRenewal{Credential: credential, Device: device, Items: items})
	})
}

// readImages hands e's signed artwork to each in bounded batches.
func (e *Entry) readImages(each func([]frontend.ImageBatchItem) error) error {
	f, err := os.Open(e.s.path(e.id, ".img"))
	if err != nil {
		return err
	}
	defer f.Close()
	in := bufio.NewReaderSize(f, 64<<10)
	str := func() (string, error) {
		n, err := binary.ReadUvarint(in)
		if err != nil || n > 4096 {
			return "", errCorrupt
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(in, b); err != nil {
			return "", errCorrupt
		}
		return string(b), nil
	}
	batch := make([]frontend.ImageBatchItem, 0, renewBatch)
	for {
		key, err := binary.ReadUvarint(in)
		if err == io.EOF {
			break
		}
		count, cerr := binary.ReadUvarint(in)
		if err != nil || cerr != nil || count == 0 || count > 16 {
			return errCorrupt
		}
		item := frontend.ImageBatchItem{Item: frontend.RatingKey(key), Authoritative: true, Images: make([]frontend.GrantedImage, count)}
		for j := range item.Images {
			var v [5]string
			for k := range v {
				if v[k], err = str(); err != nil {
					return err
				}
			}
			item.Images[j] = frontend.GrantedImage{Secret: v[0], Image: frontend.Image{Kind: v[1], Path: v[2], Tag: v[3], Subject: v[4]}}
		}
		if batch = append(batch, item); len(batch) == renewBatch {
			if err := each(batch); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		return each(batch)
	}
	return nil
}
