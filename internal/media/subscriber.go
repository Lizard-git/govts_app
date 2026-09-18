package media

func (s *subscriber) close() {
	s.closeOnce.Do(func() {
		close(s.closed)
		_ = s.pc.Close()
	})
}

func runSubscriber(s *subscriber) {
	for {
		select {
		case <-s.closed:
			return
		case packet := <-s.packets:
			if err := s.track.WriteRTP(packet); err != nil {
				s.close()
				return
			}
			s.outBytes.Add(uint64(packet.MarshalSize()))
			s.outPackets.Add(1)
		}
	}
}
