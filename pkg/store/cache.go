package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/allegro/bigcache/v3"
	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/db"

	ekocache "github.com/eko/gocache/lib/v4/cache"
	ekostore "github.com/eko/gocache/lib/v4/store"
	bcstore "github.com/eko/gocache/store/bigcache/v4"
)

var errCacheNotFound = ekostore.NotFound{}

func (s *Store) initObjectCache() error {
	cacheConfig := bigcache.DefaultConfig(24 * time.Hour)

	// 1GB by default
	// The cache size will be larger than this due to shards and other overhead, but this is a good starting point.
	// TODO understand how to set this such that we can guarantee no more than X memory
	cacheConfig.HardMaxCacheSize = 1024 * 1024 * 1024

	// configure a custom logger
	cacheConfig.Logger = log.WithPrefix("nar-info-cache").StandardLog()

	// create the cache
	bcClient, err := bigcache.New(context.Background(), cacheConfig)
	if err != nil {
		return fmt.Errorf("failed to create nar info cache: %w", err)
	}

	// wrap it with eko store and a msg pack marshaler for values
	bcStore := bcstore.NewBigcache(bcClient)

	// if we ever move to a distributed cache, we will need to add a custom marshaler
	loadFunction := func(ctx context.Context, key any) ([]byte, []ekostore.Option, error) {
		// ensure we received a path string
		path, ok := key.(string)
		if !ok {
			return nil, nil, fmt.Errorf("invalid key type: %T", key)
		}

		// ensure the path is a narinfo path
		_, pathErr := HashFromPath(path, db.ObjectTypeNarinfo)
		if pathErr != nil {
			// this isn't a narinfo path, so we can skip it
			log.Debug("skipping cache load", "path", path)
			//nolint:nilerr
			return nil, nil, nil
		}

		// look up the narinfo
		obj, getErr := s.GetObject(ctx, path)
		if errors.Is(getErr, ErrNotFound) {
			return nil, nil, ekostore.NotFound{}
		} else if err != nil {
			return nil, nil, ekostore.NotFoundWithCause(err)
		}

		// we need to consume the body
		buf, readErr := io.ReadAll(obj.Body)
		if readErr != nil {
			return nil, nil, ekostore.NotFoundWithCause(readErr)
		}

		log.Debug("loaded object for cache", "path", path)

		return buf, nil, nil
	}

	s.cache = ekocache.NewLoadable[[]byte](loadFunction, ekocache.New[[]byte](bcStore))

	return nil
}

func (s *Store) HasObjectWithCache(ctx context.Context, path string) (*Object, error) {
	return s.getObjectFromCache(ctx, path)
}

func (s *Store) GetObjectWithCache(ctx context.Context, path string) (*Object, error) {
	return s.getObjectFromCache(ctx, path)
}

func (s *Store) getObjectFromCache(ctx context.Context, path string) (*Object, error) {
	buf, err := s.cache.Get(ctx, path)
	if errors.Is(err, errCacheNotFound) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, fmt.Errorf("failed to get object from cache: %w", err)
	}

	return &Object{
		Type:        db.ObjectTypeNarinfo,
		Compression: db.CompressionTypeNone,
		Body:        bytes.NewReader(buf),
		Size:        uint64(len(buf)),
	}, nil
}
