package root

import (
	dbm "github.com/cosmos/cosmos-sdk/db"
	"github.com/cosmos/cosmos-sdk/store/iavl"
	"github.com/cosmos/cosmos-sdk/store/mem"
	v1Store "github.com/cosmos/cosmos-sdk/store/rootmulti"
	"github.com/cosmos/cosmos-sdk/store/transient"
	"github.com/cosmos/cosmos-sdk/store/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
)

// MigrateV2 will migrate the state from iavl to smt
func MigrateV2(rootMultiStore *v1Store.Store, dbConnection dbm.DBConnection, storeConfig StoreConfig) (*Store, error) {
	type namedStore struct {
		*iavl.Store
		name string
	}
	var stores []namedStore
	for key := range rootMultiStore.GetStores() {
		switch store := rootMultiStore.GetCommitKVStore(key).(type) {
		case *iavl.Store:
			err := storeConfig.RegisterSubstore(key.Name(), types.StoreTypePersistent)
			if err != nil {
				return nil, err
			}
			stores = append(stores, namedStore{name: key.Name(), Store: store})
		case *transient.Store, *mem.Store:
			continue
		default:
			return nil, sdkerrors.Wrapf(sdkerrors.ErrLogic, "don't know how to migrate store %q of type %T", key.Name(), store)
		}
	}

	// creating the new store of smt tree
	rootStore, err := NewStore(dbConnection, storeConfig)
	if err != nil {
		return nil, err
	}

	// if version is 0 there is no state data to commit
	if rootMultiStore.LastCommitID().Version == 0 {
		return rootStore, nil
	}

	// iterate through the rootmulti stores and save the key/values into smt tree
	for _, store := range stores {
		subStore, err := rootStore.getSubstore(store.name)
		if err != nil {
			return nil, err
		}
		// iterate all iavl tree node key/values
		iterator := store.Iterator(nil, nil)
		for ; iterator.Valid(); iterator.Next() {
			// set the iavl key,values into smt node
			subStore.Set(iterator.Key(), iterator.Value())
		}
	}

	// commit the all key/values from iavl to smt tree (SMT Store)
	_, err = rootStore.commit(uint64(rootMultiStore.LastCommitID().Version))
	if err != nil {
		return nil, err
	}

	return rootStore, nil
}
