
// realmain runs the Subscribe example with a real RSS fetcher.
package main

import (
	"fmt"
	"math/rand"
	"time"

	rss "github.com/muesli/go-pkg-rss"
)

// An Item is a stripped-down RSS item.
type Item struct{ Title, Channel, GUID string }


// A Fetcher fetches Items and returns the time when the next fetch should be
// attempted.  On failure, Fetch returns a non-nil error.
type Fetcher interface {
	Fetch() (items []Item, next time.Time, err error)
}


// A Subscription delivers Items over a channel.  Close cancels the
// subscription, closes the Updates channel, and returns the last fetch error,
// if any.
type Subscription interface {
	Updates() <-chan Item
	Close() error
}


// Subscribe returns a new Subscription that uses fetcher to fetch Items.
func Subscribe(fetcher Fetcher) Subscription {
	s := &sub{
		fetcher: fetcher,
		updates: make(chan Item),       // for Updates
		closing: make(chan chan error), // for Close
	}
	go s.loop()
	return s
}

// sub implements the Subscription interface.
type sub struct {
	fetcher Fetcher         // fetches items
	updates chan Item       // sends items to the user
	closing chan chan error // for Close
}

func (s *sub) Updates() <-chan Item {
	return s.updates
}

func (s *sub) Close() error {
	errc := make(chan error)
	s.closing <- errc
	return <-errc
}

// loop periodically fecthes Items, sends them on s.updates, and exits
// when Close is called.  It extends dedupeLoop with logic to run
// Fetch asynchronously.
func (s *sub) loop() {
	const maxPending = 10
	type fetchResult struct {
		fetched []Item
		next    time.Time
		err     error
	}
	var fetchDone chan fetchResult // if non-nil, Fetch is running
	var pending []Item
	var next time.Time
	var err error
	var seen = make(map[string]bool)
	for {
		var fetchDelay time.Duration
		if now := time.Now(); next.After(now) {
			fetchDelay = next.Sub(now)
		}
		var startFetch <-chan time.Time
		if fetchDone == nil && len(pending) < maxPending {
			startFetch = time.After(fetchDelay) // enable fetch case
		}
		var first Item
		var updates chan Item
		if len(pending) > 0 {
			first = pending[0]
			updates = s.updates // enable send case
		}
		select {
		case <-startFetch:
			fetchDone = make(chan fetchResult, 1)
			go func() {
				fetched, next, err := s.fetcher.Fetch()
				fetchDone <- fetchResult{fetched, next, err}
			}()
		case result := <-fetchDone:
			fetchDone = nil
			// Use result.fetched, result.next, result.err
			fetched := result.fetched
			next, err = result.next, result.err
			if err != nil {
				next = time.Now().Add(10 * time.Second)
				break
			}
			for _, item := range fetched {
				if id := item.GUID; !seen[id] {
					pending = append(pending, item)
					seen[id] = true
				}
			}
		case errc := <-s.closing:
			errc <- err
			close(s.updates)
			return
		case updates <- first:
			pending = pending[1:]
		}
	}
}

type merge struct {
	subs    []Subscription
	updates chan Item
	quit    chan struct{}
	errs    chan error
}

// Merge returns a Subscription that merges the item streams from subs.
// Closing the merged subscription closes subs.
func Merge(subs ...Subscription) Subscription {
	m := &merge{
		subs:    subs,
		updates: make(chan Item),
		quit:    make(chan struct{}),
		errs:    make(chan error),
	}
	for _, sub := range subs {
		go func(s Subscription) {
			for {
				var it Item
				select {
				case it = <-s.Updates():
				case <-m.quit:
					m.errs <- s.Close()
					return
				}
				select {
				case m.updates <- it:
				case <-m.quit:
					m.errs <- s.Close()
					return
				}
			}
		}(sub)
	}
	// STOPMERGE OMIT
	return m
}

func (m *merge) Updates() <-chan Item {
	return m.updates
}

func (m *merge) Close() (err error) {
	close(m.quit)
	for range m.subs {
		if e := <-m.errs; e != nil {
			err = e
		}
	}
	close(m.updates)
	return
}


// Fetch returns a Fetcher for Items from domain.
func Fetch(domain string) Fetcher {
	return NewFetcher(fmt.Sprintf("http://%s/feeds/posts/default?alt=rss", domain))
}


type fetcher struct {
	uri   string
	feed  *rss.Feed
	items []Item
}

// NewFetcher returns a Fetcher for uri.
func NewFetcher(uri string) Fetcher {
	f := &fetcher{
		uri: uri,
	}
	newChans := func(feed *rss.Feed, chans []*rss.Channel) {}
	newItems := func(feed *rss.Feed, ch *rss.Channel, items []*rss.Item) {
		for _, it := range items {
			f.items = append(f.items, Item{
				Channel: ch.Title,
				GUID:    it.Id,
				Title:   it.Title,
			})
		}
	}
	f.feed = rss.New(1 /*minimum interval in minutes*/, true /*respect limit*/, newChans, newItems)
	return f
}

func (f *fetcher) Fetch() (items []Item, next time.Time, err error) {
	fmt.Println("fetching", f.uri)
	if err = f.feed.Fetch(f.uri, nil); err != nil {
		return
	}
	items = f.items
	f.items = nil
	next = time.Now().Add(time.Duration(f.feed.SecondsTillUpdate()) * time.Second)
	return
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

// STARTMAIN OMIT
func main() {
	// STARTMERGECALL OMIT
	// Subscribe to some feeds, and create a merged update stream.
	merged := Merge(
		Subscribe(Fetch("blog.golang.org")),
		Subscribe(Fetch("googleblog.blogspot.com")),
		Subscribe(Fetch("googledevelopers.blogspot.com")))
	// STOPMERGECALL OMIT

	// Close the subscriptions after some time.
	time.AfterFunc(3*time.Second, func() {
		fmt.Println("closed:", merged.Close())
	})

	// Print the stream.
	for it := range merged.Updates() {
		fmt.Println(it.Channel, it.Title)
	}

	// Uncomment the panic below to dump the stack traces.  This
	// will show several stacks for persistent HTTP connections
	// created by the real RSS client.  To clean these up, we'll
	// need to extend Fetcher with a Close method and plumb this
	// through the RSS client implementation.
	//
	// panic("show me the stacks")
}
