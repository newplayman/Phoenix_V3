package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// redisdev is a small, local-only Redis-compatible server focused on the subset
// Phoenix V3 needs for rehearsal:
// - PING
// - XADD
// - XREAD
// - XTRIM (MINID)
//
// It is not a full Redis replacement. It exists so we can validate the
// Redis Streams code paths in environments without docker/redis binaries.

type message struct {
	id     string
	fields []string // [k1,v1,k2,v2...]
}

type stream struct {
	name string
	mu   sync.Mutex
	cond *sync.Cond
	msgs []message
	seq  int64
	ms   int64
}

func newStream(name string) *stream {
	s := &stream{name: name}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *stream) nextID(now time.Time) string {
	ms := now.UnixMilli()
	if ms == s.ms {
		s.seq++
	} else {
		s.ms = ms
		s.seq = 0
	}
	return fmt.Sprintf("%d-%d", s.ms, s.seq)
}

func parseID(id string) (ms, seq int64, ok bool) {
	parts := strings.Split(id, "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	ms, err1 := strconv.ParseInt(parts[0], 10, 64)
	seq, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return ms, seq, true
}

func idGreater(a, b string) bool {
	ams, aseq, aok := parseID(a)
	bms, bseq, bok := parseID(b)
	if !aok || !bok {
		return a > b
	}
	if ams != bms {
		return ams > bms
	}
	return aseq > bseq
}

type server struct {
	mu      sync.Mutex
	streams map[string]*stream
}

func newServer() *server {
	return &server{streams: map[string]*stream{}}
}

func (s *server) getStream(name string) *stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.streams[name]
	if ok {
		return st
	}
	st = newStream(name)
	s.streams[name] = st
	return st
}

func main() {
	addr := flag.String("addr", "127.0.0.1:6379", "listen address")
	flag.Parse()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()

	log.Printf("redisdev listening on %s", *addr)
	serv := newServer()
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Printf("accept failed: %v", err)
			continue
		}
		go serv.handleConn(c)
	}
}

func (s *server) handleConn(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	defer w.Flush()

	for {
		args, err := readArray(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			writeError(w, "ERR "+err.Error())
			return
		}
		if len(args) == 0 {
			continue
		}
		cmd := strings.ToUpper(args[0])
		switch cmd {
		case "PING":
			writeSimpleString(w, "PONG")
		case "QUIT":
			writeSimpleString(w, "OK")
			return
		case "ECHO":
			if len(args) < 2 {
				writeBulkString(w, "")
				break
			}
			writeBulkString(w, args[1])
		case "HELLO":
			// Minimal response compatible with RESP2 clients.
			writeArray(w, []respValue{
				bulk("server"), bulk("redis"),
				bulk("version"), bulk("7.0.0"),
				bulk("proto"), bulk("2"),
			})
		case "XADD":
			// XADD key id field value [field value...]
			if len(args) < 5 || (len(args)-3)%2 != 0 {
				writeError(w, "ERR wrong number of arguments for XADD")
				break
			}
			key := args[1]
			id := args[2]
			fields := args[3:]
			st := s.getStream(key)
			st.mu.Lock()
			if id == "*" {
				id = st.nextID(time.Now())
			}
			st.msgs = append(st.msgs, message{id: id, fields: append([]string(nil), fields...)})
			st.cond.Broadcast()
			st.mu.Unlock()
			writeBulkString(w, id)
		case "XTRIM":
			// XTRIM key MINID ~ <id> [LIMIT n]
			if len(args) < 5 {
				writeError(w, "ERR wrong number of arguments for XTRIM")
				break
			}
			key := args[1]
			mode := strings.ToUpper(args[2])
			if mode != "MINID" {
				writeInteger(w, 0)
				break
			}
			minID := args[4]
			st := s.getStream(key)
			st.mu.Lock()
			removed := 0
			kept := st.msgs[:0]
			for _, m := range st.msgs {
				if idGreater(minID, m.id) {
					removed++
					continue
				}
				kept = append(kept, m)
			}
			st.msgs = kept
			st.mu.Unlock()
			writeInteger(w, int64(removed))
		case "XREAD":
			out, err := s.handleXRead(args)
			if err != nil {
				writeError(w, "ERR "+err.Error())
				break
			}
			if out == nil {
				writeNullArray(w)
				break
			}
			writeResp(w, out)
		default:
			writeError(w, "ERR unsupported command")
		}
		_ = w.Flush()
	}
}

func (s *server) handleXRead(args []string) (respValue, error) {
	// XREAD [COUNT n] [BLOCK ms] STREAMS key [key ...] id [id ...]
	count := int64(100)
	block := time.Duration(0)
	idx := 1
	for idx < len(args) {
		sw := strings.ToUpper(args[idx])
		switch sw {
		case "COUNT":
			if idx+1 >= len(args) {
				return nil, fmt.Errorf("COUNT requires value")
			}
			v, err := strconv.ParseInt(args[idx+1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid COUNT")
			}
			count = v
			idx += 2
		case "BLOCK":
			if idx+1 >= len(args) {
				return nil, fmt.Errorf("BLOCK requires value")
			}
			v, err := strconv.ParseInt(args[idx+1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid BLOCK")
			}
			block = time.Duration(v) * time.Millisecond
			idx += 2
		case "STREAMS":
			idx++
			goto streams
		default:
			return nil, fmt.Errorf("unexpected token %s", args[idx])
		}
	}

streams:
	if idx >= len(args) {
		return nil, fmt.Errorf("missing streams")
	}
	// Remaining args: keys... ids...
	rem := args[idx:]
	if len(rem)%2 != 0 {
		return nil, fmt.Errorf("STREAMS expects keys and ids")
	}
	half := len(rem) / 2
	keys := rem[:half]
	ids := rem[half:]

	deadline := time.Now().Add(block)
	for {
		streamsReply := make([]respValue, 0)
		for i, key := range keys {
			startID := ids[i]
			st := s.getStream(key)
			st.mu.Lock()
			msgs := make([]message, 0)
			for _, m := range st.msgs {
				if idGreater(m.id, startID) {
					msgs = append(msgs, m)
					if int64(len(msgs)) >= count {
						break
					}
				}
			}
			st.mu.Unlock()
			if len(msgs) == 0 {
				continue
			}

			entries := make([]respValue, 0, len(msgs))
			for _, m := range msgs {
				fields := make([]respValue, 0, len(m.fields))
				for _, f := range m.fields {
					fields = append(fields, bulk(f))
				}
				entries = append(entries, array([]respValue{bulk(m.id), array(fields)}))
			}
			streamsReply = append(streamsReply, array([]respValue{bulk(key), array(entries)}))
		}
		if len(streamsReply) > 0 {
			return array(streamsReply), nil
		}
		if block <= 0 || time.Now().After(deadline) {
			return nil, nil
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// RESP helpers

type respValue interface{
	writeTo(w *bufio.Writer)
}

type respSimple string

func (v respSimple) writeTo(w *bufio.Writer) { fmt.Fprintf(w, "+%s\r\n", string(v)) }

type respError string

func (v respError) writeTo(w *bufio.Writer) { fmt.Fprintf(w, "-%s\r\n", string(v)) }

type respInt int64

func (v respInt) writeTo(w *bufio.Writer) { fmt.Fprintf(w, ":%d\r\n", int64(v)) }

type respBulk struct{ s string }

func (v respBulk) writeTo(w *bufio.Writer) {
	fmt.Fprintf(w, "$%d\r\n%s\r\n", len(v.s), v.s)
}

type respArray struct{ items []respValue }

func (v respArray) writeTo(w *bufio.Writer) {
	fmt.Fprintf(w, "*%d\r\n", len(v.items))
	for _, it := range v.items {
		it.writeTo(w)
	}
}

type respNullArray struct{}

func (v respNullArray) writeTo(w *bufio.Writer) { w.WriteString("*-1\r\n") }

func bulk(s string) respValue { return respBulk{s: s} }
func array(items []respValue) respValue { return respArray{items: items} }

func writeResp(w *bufio.Writer, v respValue) { v.writeTo(w) }
func writeSimpleString(w *bufio.Writer, s string) { writeResp(w, respSimple(s)) }
func writeError(w *bufio.Writer, s string) { writeResp(w, respError(s)) }
func writeInteger(w *bufio.Writer, i int64) { writeResp(w, respInt(i)) }
func writeBulkString(w *bufio.Writer, s string) { writeResp(w, respBulk{s: s}) }
func writeArray(w *bufio.Writer, items []respValue) { writeResp(w, respArray{items: items}) }
func writeNullArray(w *bufio.Writer) { writeResp(w, respNullArray{}) }

func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line, nil
}

func readArray(r *bufio.Reader) ([]string, error) {
	header, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if header != '*' {
		return nil, fmt.Errorf("expected array")
	}
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("invalid array len")
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		t, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if t != '$' {
			return nil, fmt.Errorf("expected bulk")
		}
		llen, err := readLine(r)
		if err != nil {
			return nil, err
		}
		l, err := strconv.Atoi(llen)
		if err != nil || l < 0 {
			return nil, fmt.Errorf("invalid bulk len")
		}
		buf := make([]byte, l+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:l]))
	}
	return args, nil
}

