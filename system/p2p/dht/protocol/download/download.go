package download

import (
	"context"
	"errors"
	"time"

	"github.com/33cn/chain33/common/log/log15"
	"github.com/33cn/chain33/system/p2p/dht/protocol"
	"github.com/33cn/chain33/types"
	"github.com/libp2p/go-libp2p-core/peer"
)

var (
	log = log15.New("module", "p2p.download")
)

func init() {
	protocol.RegisterProtocolInitializer(InitProtocol)
}

const (
	// Deprecated: old version, use downloadBlock instead
	downloadBlockOld = "/chain33/downloadBlockReq/1.0.0"
	downloadBlock    = "/chain33/download-block/1.0.0"
)

// Protocol ...
type Protocol struct {
	*protocol.P2PEnv
	counter *Counter
}

// InitProtocol initials protocol
func InitProtocol(env *protocol.P2PEnv) {
	p := &Protocol{
		P2PEnv:  env,
		counter: NewCounter(),
	}
	//注册p2p通信协议，用于处理节点之间请求
	protocol.RegisterStreamHandler(p.Host, downloadBlockOld, p.handleStreamDownloadBlockOld)
	protocol.RegisterStreamHandler(p.Host, downloadBlock, p.handleStreamDownloadBlock)
	//注册事件处理函数
	protocol.RegisterEventHandler(types.EventFetchBlocks, p.handleEventDownloadBlock)

}

func (p *Protocol) downloadBlock(height int64, tasks tasks) error {

	var retryCount uint
	tasks.Sort() //TODO bug 对任务节点时延进行排序，优先选择时延低的节点进行下载
ReDownload:
	select {
	case <-p.Ctx.Done():
		log.Warn("downloadBlock", "process", "done")
		return p.Ctx.Err()
	default:
		break
	}

	if tasks.Size() == 0 {
		return errors.New("no peer for download")
	}

	retryCount++
	if retryCount > 50 {
		return errors.New("beyound max try count 50")
	}

	task := p.availbTask(tasks, height)
	if task == nil {
		time.Sleep(time.Millisecond * 400)
		goto ReDownload
	}

	var downloadStart = time.Now()
	//一个高度在一个pid上请求。
	block, err := p.downloadBlockFromPeerOld(height, task.Pid)
	if err != nil {
		//发生EOF，剔除无用节点。
		//EROR[06-16|17:09:26] handleEventDownloadBlock                 module=p2p.download SendRecvPeer="stream reset" pid=16Uiu2HAkzNiDx1mN6muuBRgPpDRaUG5NGs8HMHmp1HND968Y6Kho
		log.Error("handleEventDownloadBlock", "SendRecvPeer", err, "pid", task.Pid)
		p.releaseJob(task)
		tasks = tasks.Remove(task)
		goto ReDownload
	}
	remotePid := task.Pid.Pretty()
	costTime := time.Since(downloadStart)
	p.counter.UpdateTaskInfo(task.ID, remotePid, height, costTime.Milliseconds())

	log.Debug("download+++++", "from", remotePid, "height", block.GetHeight(),
		"blockSize (bytes)", block.Size(), "costTime ms", costTime.Milliseconds())

	msg := p.QueueClient.NewMessage("blockchain", types.EventSyncBlock, &types.BlockPid{Pid: remotePid, Block: block}) //加入到输出通道)
	_ = p.QueueClient.Send(msg, false)
	p.releaseJob(task)

	return nil
}

// TODO unused
func (p *Protocol) downloadBlockFromPeer(height int64, pid peer.ID) (*types.Block, error) {
	ctx, cancel := context.WithTimeout(p.Ctx, time.Second*10)
	defer cancel()
	p.Host.ConnManager().Protect(pid, downloadBlock)
	defer p.Host.ConnManager().Unprotect(pid, downloadBlock)
	stream, err := p.Host.NewStream(ctx, pid, downloadBlock)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	blockReq := &types.ReqBlocks{Start: height, End: height}
	err = protocol.WriteStream(blockReq, stream)
	if err != nil {
		return nil, err
	}
	var block types.Block
	err = protocol.ReadStream(&block, stream)
	if err != nil {
		return nil, err
	}
	return &block, nil
}

func (p *Protocol) downloadBlockFromPeerOld(height int64, pid peer.ID) (*types.Block, error) {
	ctx, cancel := context.WithTimeout(p.Ctx, time.Second*10)
	defer cancel()
	p.Host.ConnManager().Protect(pid, downloadBlockOld)
	defer p.Host.ConnManager().Unprotect(pid, downloadBlockOld)
	stream, err := p.Host.NewStream(ctx, pid, downloadBlockOld)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	blockReq := types.MessageGetBlocksReq{
		Message: &types.P2PGetBlocks{
			StartHeight: height,
			EndHeight:   height,
		},
	}
	err = protocol.WriteStream(&blockReq, stream)
	if err != nil {
		return nil, err
	}
	var resp types.MessageGetBlocksResp
	err = protocol.ReadStream(&resp, stream)
	if err != nil {
		return nil, err
	}
	block := resp.Message.Items[0].Value.(*types.InvData_Block).Block
	return block, nil
}
