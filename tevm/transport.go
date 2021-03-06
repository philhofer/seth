package tevm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/philhofer/seth"
)

type callArgs struct {
	From     common.Address  `json:"from"`
	To       *common.Address `json:"to"`
	Gas      seth.Uint64     `json:"gas"`
	GasPrice seth.Int        `json:"gasPrice"`
	Value    seth.Int        `json:"value"`
	Data     seth.Data       `json:"data"`
}

func (c *callArgs) tx() *seth.Transaction {
	return &seth.Transaction{
		From:     (*seth.Address)(&c.From),
		To:       (*seth.Address)(c.To),
		Gas:      c.Gas,
		GasPrice: c.GasPrice,
		Value:    c.Value,
		Input:    c.Data,
	}
}

type blocknum int64

var rawlatest = []byte(`"latest"`)
var rawpending = []byte(`"pending"`)
var rawearliest = []byte(`"earliest"`)

func (b *blocknum) UnmarshalJSON(buf []byte) error {
	switch {
	case bytes.Equal(rawpending, buf):
		*b = -1
	case bytes.Equal(rawlatest, buf):
		*b = -2
	case bytes.Equal(rawearliest, buf):
		*b = 0
	default:
		// should be an integer
		i, err := strconv.ParseInt(string(buf), 10, 64)
		if err != nil {
			return err
		}
		*b = blocknum(i)
	}
	return nil
}

func (a *callArgs) Ref() vm.ContractRef {
	return (*acctref)(&a.From)
}

// ServeHTTP implements http.Handler.
func (s *Chain) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var jsr seth.RPCRequest
	err := json.NewDecoder(r.Body).Decode(&jsr)
	if err != nil {
		log.Printf("decode body error: %s", err)
		w.WriteHeader(401)
		return
	}
	var res seth.RPCResponse
	s.Execute(&jsr, &res)
	err = json.NewEncoder(w).Encode(&res)
	if err != nil {
		log.Printf("error writing response: %s", err)
		w.WriteHeader(500)
		return
	}
}

// Execute implements seth.Transport.
func (c *Chain) Execute(req *seth.RPCRequest, res *seth.RPCResponse) error {
	res.ID = req.ID
	res.Version = req.Version

	if c.Debugf != nil {
		c.Debugf("request:\n%s\n", pretty(req))
	}

	c.mu.Lock()
	ret, err := c.execute(req.Method, req.Params)
	c.mu.Unlock()
	if err != nil {
		res.Result = nil
		res.Error.Code = -32601
		res.Error.Message = err.Error()
		res.Error.Data = nil
		err = nil
	} else {
		err = gross(ret, &res.Result)
	}

	if c.Debugf != nil {
		c.Debugf("response:\n%s\n", pretty(res))
	}
	return err
}

func (c *Chain) execute(method string, params []json.RawMessage) (interface{}, error) {
	var b blocknum
	switch method {
	case "eth_protocolVersion":
		if err := marshal(params); err != nil {
			return nil, err
		}
		return seth.Uint64(63), nil
	case "eth_syncing":
		if err := marshal(params); err != nil {
			return nil, err
		}
		return false, nil
	case "eth_gasPrice":
		if err := marshal(params); err != nil {
			return nil, err
		}
		return seth.Uint64(16e9), nil
	case "eth_blockNumber":
		if err := marshal(params); err != nil {
			return nil, err
		}
		return c.State.Pending.Number, nil
	case "eth_call":
		tx := new(callArgs)
		if err := marshal(params, tx, &b); err != nil {
			return nil, err
		}
		return c.staticCall(tx, int64(b))
	case "eth_sendTransaction":
		a := new(callArgs)
		if err := marshal(params, a); err != nil {
			return nil, err
		}
		return c.send(a.tx())
	case "eth_getTransactionReceipt":
		var h seth.Hash
		if err := marshal(params, &h); err != nil {
			return nil, err
		}
		return c.receipt(h)
	case "eth_getTransactionByHash":
		var h seth.Hash
		if err := marshal(params, &h); err != nil {
			return nil, err
		}
		return c.transaction(h)
	case "eth_getBalance":
		var addr seth.Address
		if err := marshal(params, &addr, &b); err != nil {
			return nil, err
		}
		return c.balance(&addr, int64(b))
	case "eth_estimateGas":
		a := new(callArgs)
		if err := marshal(params, a, &b); err != nil {
			return nil, err
		}
		return c.estimate(a, int64(b))
	case "eth_getBlockByHash":
		var h seth.Hash
		var all bool
		if err := marshal(params, &h, &all); err != nil {
			return nil, err
		}
		return c.getBlock(&h, all)
	case "eth_getBlockByNumber":
		var n seth.Int
		var all bool
		if err := marshal(params, &n, &all); err != nil {
			return nil, err
		}
		// hack: block hashes are hashes of the block number
		h := seth.Hash(n2h(uint64(n.Int64())))
		return c.getBlock(&h, all)
	case "eth_newFilter":
		type newFilterReq struct {
			FromBlock blocknum      `json:"fromBlock,omitempty"`
			ToBlock   blocknum      `json:"toBlock,omitempty"`
			Address   *seth.Address `json:"address,omitempty"`
			Topics    []*seth.Hash  `json:"topics,omitempty"`
		}
		req := new(newFilterReq)
		if err := marshal(params, req); err != nil {
			return nil, err
		}
		return c.newFilter(req.FromBlock, req.ToBlock, req.Address, req.Topics)
	case "eth_getFilterChanges":
		var n seth.Int
		if err := marshal(params, &n); err != nil {
			return nil, err
		}
		return c.filterChanges(int(n.Int64()))
	case "eth_getFilterLogs":
		var n seth.Int
		if err := marshal(params, &n); err != nil {
			return nil, err
		}
		return c.filterLogs(int(n.Int64()))
	case "eth_uninstallFilter":
		var n seth.Int
		if err := marshal(params, &n); err != nil {
			return nil, err
		}
		return c.deleteFilter(int(n.Int64()))
	default:
		return nil, errors.New(method + ": unsupported method")
	}
}

func (c *Chain) newFilter(from, to blocknum, addr *seth.Address, topics []*seth.Hash) (int, error) {
	if from > to {
		return 0, fmt.Errorf("cannot filter block range [%d,%d)", from, to)
	}
	c.filtcount++
	if c.filters == nil {
		c.filters = make(map[int]*filter)
	}
	c.filters[c.filtcount] = &filter{
		from:   from,
		to:     to,
		addr:   addr,
		topics: topics,
	}
	return c.filtcount, nil
}

func (c *Chain) filterLogs(fd int) ([]seth.Log, error) {
	// unlike filterChanges, this is supposed
	// to yield every matching entry to the filter
	if c.filters == nil {
		return nil, fmt.Errorf("bad filter id %d", fd)
	}
	filt, ok := c.filters[fd]
	if !ok {
		return nil, fmt.Errorf("bad filter id %d", fd)
	}

	out := make([]seth.Log, 0)
	for i := range c.State.Logs {
		if filt.matches(c.State.Logs[i]) {
			var next seth.Log
			l2l(c.State.Logs[i], &next)
			out = append(out, next)
		}
	}
	return out, nil
}

func (c *Chain) filterChanges(fd int) ([]seth.Log, error) {
	if c.filters == nil {
		return nil, fmt.Errorf("bad filter id %d", fd)
	}
	filt, ok := c.filters[fd]
	if !ok {
		return nil, fmt.Errorf("bad filter id %d", fd)
	}

	out := make([]seth.Log, 0)
	sub := c.State.Logs[filt.lastlog:]
	for i := range sub {
		if filt.matches(sub[i]) {
			var next seth.Log
			l2l(sub[i], &next)
			out = append(out, next)
		}
	}
	filt.lastlog += len(sub)
	return out, nil
}

func (c *Chain) deleteFilter(fd int) (bool, error) {
	l := len(c.filters)
	delete(c.filters, fd)
	return len(c.filters) != l, nil
}

// staticCall handles eth_call.
func (c *Chain) staticCall(a *callArgs, blocknum int64) (seth.Data, error) {
	c = c.AtBlock(blocknum)
	if c == nil {
		return nil, fmt.Errorf("unknown block number %d", blocknum)
	}
	evm := c.evm(a.From)
	gas := uint64(c.State.Pending.GasLimit)
	if a.Gas != 0 {
		gas = uint64(a.Gas)
	}
	var to common.Address
	if a.To != nil {
		to = *a.To
	}
	ret, _, err := evm.StaticCall(a.Ref(), to, a.Data, gas)
	if err != nil {
		return nil, err
	}
	return seth.Data(ret), nil
}

func (c *Chain) getBlock(h *seth.Hash, fulltx bool) (*seth.Block, error) {
	var b *seth.Block
	if bytes.Equal(h[:], c.State.Pending.Hash[:]) {
		b = c.State.Pending
	} else {
		buf := c.State.Blocks.Get(h[:])
		if buf == nil {
			return nil, fmt.Errorf("unknown block hash %s", h)
		}
		b = new(seth.Block)
		if _, err := b.UnmarshalMsg(buf); err != nil {
			return nil, err
		}
	}
	if !fulltx {
		return b, nil
	}

	// populate the block transactions appropriately
	var txh seth.Hash
	var tx seth.Transaction
	ntx := make([]json.RawMessage, 0, len(b.Transactions))
	for i := range b.Transactions {
		err := json.Unmarshal(b.Transactions[i], &txh)
		if err != nil {
			return nil, fmt.Errorf("internal error: malformed tx %q", b.Transactions[i])
		}
		txbuf := c.State.Transactions.Get(txh[:])
		if txbuf == nil {
			return nil, fmt.Errorf("internal error: no such tx %q", txh)
		}
		tx = seth.Transaction{}
		if _, err := tx.UnmarshalMsg(txbuf); err != nil {
			return nil, err
		}
		ntx = append(ntx, js(tx))
	}
	b.Transactions = ntx
	return b, nil
}

// send handles eth_sendTransaction
func (c *Chain) send(a *seth.Transaction) (*seth.Hash, error) {
	_, h, err := c.Mine(a)
	if err != nil {
		return nil, err
	}
	// For now, 1 tx per block.
	c.Seal()
	return &h, nil
}

// receipt handles eth_getTransactionReceipt.
func (c *Chain) receipt(h seth.Hash) (*seth.Receipt, error) {
	b := c.State.Receipts.Get(h[:])
	if b == nil {
		return nil, fmt.Errorf("receipt %s not found", h.String())
	}
	r := new(seth.Receipt)
	_, err := r.UnmarshalMsg(b)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// transaction handles eth_getTransactionByHash.
func (c *Chain) transaction(h seth.Hash) (*seth.Transaction, error) {
	b := c.State.Transactions.Get(h[:])
	if b == nil {
		return nil, fmt.Errorf("no such transaction %s", h.String())
	}
	tx := new(seth.Transaction)
	_, err := tx.UnmarshalMsg(b)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

// balance handles eth_getBalance.
func (c *Chain) balance(addr *seth.Address, block int64) (*seth.Int, error) {
	c = c.AtBlock(block)
	if c == nil {
		return nil, fmt.Errorf("unknown block number %d", block)
	}
	return (*seth.Int)(c.balanceOf(addr)), nil
}

// estimate handles eth_estimateGas.
func (c *Chain) estimate(a *callArgs, blocknum int64) (seth.Uint64, error) {
	c = c.AtBlock(blocknum)
	if c == nil {
		return 0, fmt.Errorf("unknown block number %d", blocknum)
	}
	evm := c.evm(a.From)

	// TODO: incorporate gas price
	evm.Context.GasPrice = big.NewInt(0)
	snap := evm.StateDB.Snapshot()

	gas := uint64(c.State.Pending.GasLimit)
	if a.Gas != 0 {
		gas = uint64(a.Gas)
	}

	if a.To == nil {
		_, _, rem, err := evm.Create(a.Ref(), a.Data, gas, a.Value.Big())
		if err != nil {
			return 0, err
		}
		gas -= rem
	} else {
		_, rem, err := evm.Call(a.Ref(), *a.To, a.Data, gas, a.Value.Big())
		if err != nil {
			return 0, err
		}
		gas -= rem
	}

	evm.StateDB.RevertToSnapshot(snap)

	return seth.Uint64(gas), nil
}

func marshal(from []json.RawMessage, to ...interface{}) error {
	if len(from) != len(to) {
		return fmt.Errorf("expected %d params; found %d", len(to), len(from))
	}
	for i := range from {
		gross(from[i], to[i])
	}
	return nil
}

func gross(x, y interface{}) error {
	b, err := json.Marshal(x)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, y)
}
