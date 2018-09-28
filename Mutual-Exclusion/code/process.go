package mutual

import (
	"github.com/aQuaYi/observer"
)

// OTHERS 表示信息接收方为其他所有 process
const OTHERS = -1

type process struct {
	me          int
	isOccupying bool
	occupyTimes int // process 可以占用资源的次数

	clock            Clock
	resource         Resource
	requestTimestamp Timestamp
	receivedTime     ReceivedTime
	requestQueue     RequestQueue

	prop   observer.Property
	stream observer.Stream
}

func newProcess(all, me int, r Resource, prop observer.Property) *process {
	p := &process{
		me:               me,
		resource:         r,
		prop:             prop,
		clock:            newClock(),
		requestQueue:     newRequestQueue(),
		receivedTime:     newReceivedTime(all, me),
		requestTimestamp: nil,
	}

	go p.Listening()

	debugPrintf("[%d]P%d 完成创建工作", p.clock.Now(), p.me)

	return p
}

func (p *process) request() {
	ts := newTimestamp(p.clock.Tick(), p.me)
	msg := newMessage(requestResource, p.clock.Tick(), p.me, OTHERS, ts)
	// Rule 1: 发送申请信息给其他的 process
	p.prop.Update(msg)
	p.requestQueue.Push(ts)
}

func (p *process) occupyResource() {
	p.isOccupying = true
	p.resource.Occupy(p.requestTimestamp)
}

func (p *process) releaseResource() {
	ts := p.requestTimestamp
	// rule 3: 先释放资源
	p.resource.Release(ts)
	// rule 3: 在 requestQueue 中删除 ts
	p.requestQueue.Remove(ts)
	// rule 3: 把释放的消息发送给其他 process
	msg := newMessage(releaseResource, p.clock.Tick(), p.me, OTHERS, ts)
	p.prop.Update(msg)

	p.requestTimestamp = nil
	p.isOccupying = false
}

func (p *process) addOccupyTimes(n int) {
	if n < 0 {
		panic("addOccupyTimes n should be >= 0")
	}
	p.occupyTimes += n
}

func (p *process) needResource() bool {
	if p.occupyTimes <= 0 ||
		p.requestTimestamp != nil {
		return false
	}
	return true
}

func (p *process) handleRequestMessage(msg *message) {
	if msg.from == p.me {
		return
	}
	// 收到消息，总是先更新自己的时间
	p.updateClock(msg.from, msg.msgTime)
	// rule 2: 把 msg.timestamp 放入自己的 requestQueue 当中
	p.requestQueue.Push(msg.timestamp)
	// rule 2: 给对方发送一条 acknowledge 消息
	p.prop.Update(newMessage(
		acknowledgment,
		p.clock.Tick(),
		p.me,
		msg.from,
		nil,
	))
	p.checkRule5()
}

func (p *process) handleReleaseMessage(msg *message) {
	if msg.from == p.me {
		return
	}
	// 收到消息，总是先更新自己的时间
	p.updateClock(msg.from, msg.msgTime)
	// rule 4: 收到就从 request queue 中删除相应的申请
	p.requestQueue.Remove(msg.timestamp)
	p.checkRule5()
}

func (p *process) handleAcknowledgeMessage(msg *message) {
	if msg.to != p.me {
		return
	}
	// 收到消息，总是先更新自己的时间
	p.updateClock(msg.from, msg.msgTime)
	p.checkRule5()
}

func (p *process) updateClock(id, time int) {
	p.clock.Update(time)
	p.receivedTime.Update(id, time)
}

func (p *process) checkRule5() {
	if !p.requestTimestamp.isEqual(p.requestQueue.Min()) ||
		p.requestTimestamp.Time() >= p.receivedTime.Min() {
		return
	}

	// 此时，满足了 rule 5
	go func() {
		p.occupyResource()
		randSleep()
		p.releaseResource()
	}()
}

func (p *process) Listening() {
	stream := p.prop.Observe()
	for {
		msg := stream.Next().(*message)
		switch msg.msgType {
		case requestResource:
			p.handleRequestMessage(msg)
		case releaseResource:
			p.handleReleaseMessage(msg)
		case acknowledgment:
			p.handleAcknowledgeMessage(msg)
		}
	}
}
