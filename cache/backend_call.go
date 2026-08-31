package cache

import (
	"context"
)

func backendGet(backend Backend, ctx context.Context, address Address, limit ReadLimit) (value []byte, found bool, err error) {
	defer func() {
		if recover() != nil {
			value = nil
			found = false
			err = ErrBackend
		}
	}()
	value, found, err = backend.Get(ctx, address, limit)
	if err != nil {
		value = nil
		found = false
		err = sanitizedError(err, ErrBackend)
	}
	return value, found, err
}

func backendGetMany(reader BatchReader, ctx context.Context, addresses []Address, limit BatchReadLimit) (values map[Address][]byte, err error) {
	defer func() {
		if recover() != nil {
			values = nil
			err = ErrBackend
		}
	}()
	values, err = reader.GetMany(ctx, addresses, limit)
	if err != nil {
		values = nil
		err = sanitizedError(err, ErrBackend)
	}
	return values, err
}

func backendPut(backend Backend, ctx context.Context, address Address, value []byte, expiry Expiry) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrBackend
		}
	}()
	err = backend.Put(ctx, address, value, expiry)
	return sanitizedError(err, ErrBackend)
}

func backendDelete(backend Backend, ctx context.Context, address Address) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrBackend
		}
	}()
	err = backend.Delete(ctx, address)
	return sanitizedError(err, ErrBackend)
}
