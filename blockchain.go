package main

import (
	"fmt"

	"github.com/boltdb/bolt"
)

const (
	DBFILE      = "blockchain.db"
	BLOCKBUCKET = "blocks"
)

type Blockchain struct {
	topHash []byte   // 最新区块的哈希值
	db      *bolt.DB // 数据库
}

type BlockchainIterator struct {
	currentHash []byte   // 当前区块的哈希值
	db          *bolt.DB // 数据库
}

// ------------------------- Blockchain -------------------------

// GetBlockchain returns the latest block hash
//
// 返回最新区块的哈希值
func (bc *Blockchain) GetTopHash() []byte {
	return bc.topHash
}

// CreateBlockchain creates a new blockchain DB
//
// 创建一个新的区块链并且添加一个创世区块
func CreateBlockchain() *Blockchain {
	// 0600 文件拥有者具有读写权限，其他人无任何权限
	boltDB, err := bolt.Open(DBFILE, 0600, nil)
	if err != nil {
		panic(err)
	}

	var tophash []byte // 最新区块的哈希值
	// update the blockchain
	err = boltDB.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BLOCKBUCKET))

		// if bucket is nil, then create a new blockchain
		if bucket == nil {
			// create a genesisblock
			genesisBlock := GenesisBlock()

			// 创建一个新的bucket
			bucket, err = tx.CreateBucket([]byte(BLOCKBUCKET))
			if err != nil {
				panic(err)
			}

			// put the genesis block hash and genesis block into the bucket
			err = bucket.Put(genesisBlock.Hash, genesisBlock.Serialize())
			if err != nil {
				panic(err)
			}
			// put the genesis block hash and latest into the bucket
			err = bucket.Put([]byte("latest"), genesisBlock.Hash)
			if err != nil {
				panic(err)
			}
			tophash = genesisBlock.Hash

		} else {
			// genesis block already exists,
			// get the latest block hash
			tophash = bucket.Get([]byte("latest"))
		}
		return nil
	})

	if err != nil {
		panic(err)
	}

	blockchain := Blockchain{topHash: tophash, db: boltDB}

	return &blockchain
}

// AddBlock update the latest block into the blockchain
//
// 根据最新区块的哈希值和交易列表，创建一个新的区块，并更新区块链
func (bc *Blockchain) AddBlock(txs []*Transaction) bool {
	var tophash []byte

	// get the latest block hash
	err := bc.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BLOCKBUCKET))
		tophash = bucket.Get([]byte("latest"))
		return nil
	})
	if err != nil {
		panic(err)
	}

	// create a new block according to the latest block hash and transactions
	newBlock := NewBlock(tophash, txs)

	// update the blockchain
	bc.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BLOCKBUCKET))
		// put the new block and block hash into the bucket
		err := bucket.Put(newBlock.Hash, newBlock.Serialize())
		if err != nil {
			panic(err)
		}

		// update the latest block hash
		err = bucket.Put([]byte("latest"), newBlock.Hash)
		if err != nil {
			panic(err)
		}

		// update the latest block hash
		bc.topHash = newBlock.Hash

		return nil
	})

	if err != nil {
		panic(err)
	}

	return true
}

// ---------------------------- 以下是区块链迭代器 ----------------------------

// Iterator returns a BlockchainIterator
//
// 创建一个区块链迭代器
func (bc *Blockchain) Iterator() *BlockchainIterator {
	return &BlockchainIterator{bc.topHash, bc.db}
}

// Next returns the next block of the blockchain according to the current hash
//
// 返回区块链的下一个区块
func (bit *BlockchainIterator) Next() *Block {
	var block *Block

	// get the block from the database
	// view method does not allow to modify the database
	err := bit.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(BLOCKBUCKET))
		// get a block according to the current hash
		serializedBlock := bucket.Get(bit.currentHash)
		block = Deserialize(serializedBlock)
		return nil
	})
	if err != nil {
		panic(err)
	}

	// update the current hash
	bit.currentHash = block.PrevBlockHash

	return block
}

// IterateBlockchain iterates the blockchain
//
// 迭代区块链并且打印
func (bc *Blockchain) IterateBlockchain() {
	iterator := bc.Iterator()
	for {
		block := iterator.Next()
		fmt.Printf("Block number: %v\n", block.String())

		// when the previous block hash is empty, then the genesis block is reached
		if len(block.PrevBlockHash) == 0 {
			break
		}
	}

}

// FindUnspendTransaction finds all unspend transactions according to the address
//
// 根据给定的地址，找到这个地址所没有花费的输出所在的交易
func (bc *Blockchain) FindUnspendTransaction(address string) []*Transaction {
	// 关于addr的所有未花费的交易，在这些交易中一定包含有某个output是属于addr的
	// 但是，这些交易中可能还有其他output也是属于addr的，所以我们需要遍历这些交易，找到所有属于addr的output
	unsepentTXs := []*Transaction{}

	// 存储一笔交易中所有被使用的输出; map[交易ID][]int, []int对应的是交易中的输出索引
	spendTxos := make(map[string][]int)

	bcIterator := bc.Iterator()

	for {
		// iterate over all blocks from the newest to the oldest
		block := bcIterator.Next()

		// iterate over all transactions in one block
		for _, tx := range block.Transactions {
			txID := string(tx.ID)

			// iterate over all outputs in one transaction
		Outputs:
			for outIdx, output := range tx.Out {
				// if spendTxos[txID] != nil, it means that some outputs in this transaction have been used
				if spendTxos[txID] != nil {
					// iterate over all used outputs in spendTxos[txID] to check whether the output has been used
					for _, spentOutput := range spendTxos[txID] {
						// it means that the outIdx has been used
						if spentOutput == outIdx {
							continue Outputs // outIdx has been used, so we skip to the next outIdx
						}
					}
				}

				// if the outout not been used,
				// if the output can be unlocked by the address,
				// it means that the address has not spent this output
				if output.CanBeUnlockedWith(address) {
					// eg. tx #3 有3笔输出，其中第一笔输出被使用了，那么spendTxos[tx #3] = []int{0}
					// 剩下的两笔输出中只有第二笔是给bob的，所以unsepentTXs = []*Transaction{tx #3}
					// 说明tx #3中存在关于bob的未花费输出
					unsepentTXs = append(unsepentTXs, tx)
				}
			}

			// tx can have input only if it is not a coinbase transaction
			if !tx.IsCoinbase() {
				for _, input := range tx.In {
					// if the input can unlock the output with the address,
					// it means that the address has spent the output
					if input.CanUnlockOutputWith(address) {
						inputTxID := string(input.TXid)
						// inputTxID 记录了上一笔交易的ID
						// input.Voutindex 记录了上一笔交易中的具体哪一笔输出被使用了
						spendTxos[inputTxID] = append(spendTxos[inputTxID], input.Voutindex) // 记录这个交易中被使用的输出
					}
				}
			}

		}

		// 如果到了创世区块，停止遍历, 创世区块的PrevBlockHash是空的
		if len(block.PrevBlockHash) == 0 {
			break
		}
	}

	return unsepentTXs
}

// FindUTXO finds all unspent transaction outputs according to the address
//
// 根据给定的地址，找到这个地址在当前区块链中所没有花费的输出，需要使用FindUnspendTransaction函数
func (bc *Blockchain) FindUTXO(addr string) []*TXoutput {
	UTXOs := []*TXoutput{}

	unspentTxs := bc.FindUnspendTransaction(addr)

	// iterate over all transactions
	for _, tx := range unspentTxs {
		// iterate over all outputs in one transaction
		for _, output := range tx.Out {
			// if the output can be unlocked by the address,
			// it means that this output belongs to the address
			if output.CanBeUnlockedWith(addr) {
				UTXOs = append(UTXOs, &output)
			}
		}
	}

	return UTXOs
}
