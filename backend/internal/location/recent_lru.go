package location

import (
	"container/list"
	"fmt"

	"uber-test/backend/internal/model"
)

type recentLocationLRU struct {
	capacity int
	items    *list.List
	index    map[string]*list.Element
}

type recentLocationEntry struct {
	key      string
	location model.DriverLocation
}

func newRecentLocationLRU(capacity int) *recentLocationLRU {
	if capacity <= 0 {
		capacity = 20
	}

	return &recentLocationLRU{
		capacity: capacity,
		items:    list.New(),
		index:    make(map[string]*list.Element),
	}
}

func (l *recentLocationLRU) Add(location model.DriverLocation) {
	key := recentLocationKey(location)
	if element, ok := l.index[key]; ok {
		entry := element.Value.(*recentLocationEntry)
		entry.location = location
		l.items.MoveToBack(element)
		return
	}

	element := l.items.PushBack(&recentLocationEntry{
		key:      key,
		location: location,
	})
	l.index[key] = element

	if l.items.Len() > l.capacity {
		l.removeFront()
	}
}

func (l *recentLocationLRU) List() []model.DriverLocation {
	items := make([]model.DriverLocation, 0, l.items.Len())
	for element := l.items.Front(); element != nil; element = element.Next() {
		items = append(items, element.Value.(*recentLocationEntry).location)
	}
	return items
}

func (l *recentLocationLRU) removeFront() {
	element := l.items.Front()
	if element == nil {
		return
	}

	entry := element.Value.(*recentLocationEntry)
	delete(l.index, entry.key)
	l.items.Remove(element)
}

func recentLocationKey(location model.DriverLocation) string {
	return fmt.Sprintf(
		"%s|%s|%.7f|%.7f|%d",
		location.DriverID,
		location.OrderID,
		location.Lat,
		location.Lng,
		location.Timestamp.UnixMilli(),
	)
}
