package mutualexclusion

import (
	"fmt"
	"sync"

	"github.com/aQuaYi/observer"
)

// OTHERS 表示信息接收方为其他所有 process
const OTHERS = -1

// Process 是进程的接口
type Process interface {
	// Request 会申请占用资源
	// 如果上次 Request 后，还没有占用并释放资源，会发生阻塞
	// 非线程安全
	Request()
}

type process struct {
	me int            // process 的 ID
	wg sync.WaitGroup // 阻塞 Request() 用

	clock        Clock
	resource     Resource
	receivedTime ReceivedTime
	requestQueue RequestQueue

	mutex sync.Mutex
	// 为了保证发送消息的原子性，
	// 从生成 timestamp 开始到 prop.update 完成，这个过程需要上锁
	prop observer.Property
	// 操作以下属性，需要加锁
	isOccupying      bool
	requestTimestamp Timestamp
}

func (p *process) String() string {
	return fmt.Sprintf("[%d]P%d", p.clock.Now(), p.me)
}

func newProcess(all, me int, r Resource, prop observer.Property) Process {
	p := &process{
		me:           me,
		resource:     r,
		prop:         prop,
		clock:        newClock(),
		requestQueue: newRequestQueue(),
		receivedTime: newReceivedTime(all, me),
	}

	p.Listening()

	debugPrintf("%s 完成创建工作", p)

	return p
}

func (p *process) Listening() {
	// stream 的观察起点位置，由上层调用 newProcess 的方式决定
	// 在生成完所有的 process 后，再更新 prop，
	// 才能保证所有的 process 都能收到全部消息
	stream := p.prop.Observe()

	debugPrintf("%s 获取了 stream 开始监听", p)

	go func() {
		for {
			msg := stream.Next().(*message)
			if msg.from == p.me ||
				(msg.msgType == acknowledgment && msg.to != p.me) {
				// 忽略不该看见的消息
				continue
			}

			p.updateTime(msg.from, msg.msgTime)

			switch msg.msgType {
			// case acknowledgment: 收到此类消息只用更新时钟，前面已经做了
			case requestResource:
				p.handleRequestMessage(msg)
			case releaseResource:
				p.handleReleaseMessage(msg)
			}
			p.checkRule5()
		}
	}()
}

func (p *process) updateTime(from, time int) {
	p.mutex.Lock()

	// 收到消息的第一件，更新自己的 clock
	p.clock.Update(time)
	// 然后为了 Rule5(ii) 记录收到消息的时间
	// NOTICE: 接收时间一定要是对方发出的时间
	p.receivedTime.Update(from, time)

	p.mutex.Unlock()
}

func (p *process) handleRequestMessage(msg *message) {

	// rule 2.1: 把 msg.timestamp 放入自己的 requestQueue 当中
	p.requestQueue.Push(msg.timestamp)

	debugPrintf("%s 添加了 %s 后的 request queue 是 %s", p, msg.timestamp, p.requestQueue)

	p.mutex.Lock()

	// rule 2.2: 给对方发送一条 acknowledge 消息
	p.prop.Update(newMessage(
		acknowledgment,
		p.clock.Tick(),
		p.me,
		msg.from,
		msg.timestamp,
	))

	p.mutex.Unlock()
}

func (p *process) handleReleaseMessage(msg *message) {
	// rule 4: 从 request queue 中删除相应的申请
	p.requestQueue.Remove(msg.timestamp)
	debugPrintf("%s 删除了 %s 后的 request queue 是 %s", p, msg.timestamp, p.requestQueue)
}

func (p *process) checkRule5() {
	p.mutex.Lock()
	if p.isSatisfiedRule5() {
		p.occupyResource()
		go func() {
			// process 释放资源的时机交给 goroutine 调度
			p.releaseResource()
		}()
	}
	p.mutex.Unlock()
}

func (p *process) isSatisfiedRule5() bool {
	// 利用 checkRule5 的锁进行锁定
	return !p.isOccupying && // 还没有占领资源
		p.requestTimestamp != nil && // 已经申请资源
		p.requestTimestamp.IsEqual(p.requestQueue.Min()) && // Rule5.1 申请排在第一位
		p.requestTimestamp.IsBefore(p.receivedTime.Min()) // Rule5.2: 申请后，收到全部回复
}

func (p *process) occupyResource() {
	// 利用 checkRule5 的锁进行锁定
	debugPrintf("%s 准备占用资源 %s", p, p.requestQueue)
	p.isOccupying = true
	p.resource.Occupy(p.requestTimestamp)
}

func (p *process) releaseResource() {
	p.mutex.Lock()

	ts := p.requestTimestamp
	// rule 3: 先释放资源
	p.resource.Release(ts)
	// rule 3: 在 requestQueue 中删除 ts
	p.requestQueue.Remove(ts)
	// rule 3: 把释放的消息发送给其他 process
	msg := newMessage(releaseResource, p.clock.Tick(), p.me, OTHERS, ts)
	p.prop.Update(msg)
	p.isOccupying = false
	p.requestTimestamp = nil

	p.mutex.Unlock()

	p.wg.Done()
}

func (p *process) Request() {
	p.wg.Wait()
	p.wg.Add(1)

	p.mutex.Lock()

	p.clock.Tick() // 做事之前，先更新 clock
	ts := newTimestamp(p.clock.Now(), p.me)
	msg := newMessage(requestResource, p.clock.Now(), p.me, OTHERS, ts)
	// Rule 1.1: 发送申请信息给其他的 process
	p.prop.Update(msg)
	// Rule 1.2: 把申请消息放入自己的 request queue
	p.requestQueue.Push(ts)
	// 修改辅助属性，便于后续检查
	p.requestTimestamp = ts

	p.mutex.Unlock()
}
