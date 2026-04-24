// sim/realworld_replay.go
// Copyright(c) 2022-2025 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/math"
)

type realWorldReplay struct {
	events      []replayTrackEvent
	active      map[string]replayTrackState
	cursor      int
	start       float64
	end         float64
	simStart    Time
	simStartSet bool
}

type replayTrackState struct {
	callsign     av.ADSBCallsign
	squawk       av.Squawk
	lat          float32
	lon          float32
	altitude     float32
	cps          string
	hasFlightPlan bool
	acType       string
	assignedAlt  float32
	scratchpad   string
	scratchpad2  string
	entryFix     string
	exitFix      string
	depAirport   string
	destAirport  string
	rules        av.FlightRules
}

type replayTrackEvent struct {
	timestamp float64
	key       string
	delete    bool
	state     replayTrackState
}

type replayNDJSONRecord struct {
	Timestamp           float64        `json:"timestamp"`
	MessageType         string         `json:"messageType"`
	JMSMessageID        string         `json:"jmsMessageId"`
	Acid                string         `json:"acid"`
	BeaconCode          string         `json:"beaconCode"`
	CPS                 string         `json:"cps"`
	AircraftType        string         `json:"aircraftType"`
	AssignedAltitudeFt  *float64       `json:"assignedAltitudeFt"`
	SecondaryScratchpad string         `json:"secondaryScratchpad"`
	Latitude            *float64       `json:"latitude"`
	Longitude           *float64       `json:"longitude"`
	AltitudeFt          *float64       `json:"altitudeFt"`
	Extracted           map[string]any `json:"extracted"`
}

func loadRealWorldReplay(path string) (*realWorldReplay, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []replayTrackEvent
	var start, end float64
	haveAny := false

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var rec replayNDJSONRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}

		event, ok := makeReplayTrackEvent(rec)
		if !ok {
			continue
		}

		events = append(events, event)
		if !haveAny {
			start, end = event.timestamp, event.timestamp
			haveAny = true
		} else {
			if event.timestamp < start {
				start = event.timestamp
			}
			if event.timestamp > end {
				end = event.timestamp
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("no replay track events found")
	}

	sort.SliceStable(events, func(i, j int) bool {
		return events[i].timestamp < events[j].timestamp
	})

	return &realWorldReplay{
		events: events,
		active: make(map[string]replayTrackState),
		start:  start,
		end:    end,
	}, nil
}

func makeReplayTrackEvent(rec replayNDJSONRecord) (replayTrackEvent, bool) {
	if rec.Timestamp <= 0 {
		return replayTrackEvent{}, false
	}

	key := replayTrackKey(rec)
	if key == "" {
		return replayTrackEvent{}, false
	}

	if replayRecordIsDelete(rec) {
		return replayTrackEvent{timestamp: rec.Timestamp, key: key, delete: true}, true
	}

	lat, okLat := replayRecordLatitude(rec)
	lon, okLon := replayRecordLongitude(rec)
	if !okLat || !okLon {
		return replayTrackEvent{}, false
	}

	// hasFlightPlan: true when we have a real callsign (key starts with "acid:")
	hasFlightPlan := strings.HasPrefix(key, "acid:")
	callsign := replayRecordCallsign(rec, key)
	if callsign == "" {
		return replayTrackEvent{}, false
	}

	state := replayTrackState{
		callsign:      av.ADSBCallsign(callsign),
		lat:           lat,
		lon:           lon,
		altitude:      replayRecordAltitude(rec),
		cps:           replayRecordCPS(rec),
		hasFlightPlan: hasFlightPlan,
		acType:        replayRecordAcType(rec),
		assignedAlt:   replayRecordAssignedAlt(rec),
		scratchpad:    replayRecordScratchpad1(rec),
		scratchpad2:   replayRecordScratchpad2(rec),
		entryFix:      replayRecordEntryFix(rec),
		exitFix:       replayRecordExitFix(rec),
		depAirport:    replayRecordDepAirport(rec),
		destAirport:   replayRecordDestAirport(rec),
		rules:         replayRecordFlightRules(rec),
	}
	if sq, ok := replayRecordSquawk(rec); ok {
		state.squawk = sq
	}

	return replayTrackEvent{
		timestamp: rec.Timestamp,
		key:       key,
		state:     state,
	}, true
}

func replayRecordCallsign(rec replayNDJSONRecord, key string) string {
	if rec.Acid != "" {
		return strings.ToUpper(strings.TrimSpace(rec.Acid))
	}
	if acid, ok := replayExtractedString(rec.Extracted, "acid"); ok && acid != "" {
		return strings.ToUpper(strings.TrimSpace(acid))
	}
	if trackNum, ok := replayExtractedString(rec.Extracted, "trackNum"); ok && trackNum != "" {
		return "TRK" + strings.TrimSpace(trackNum)
	}
	return strings.ToUpper(strings.TrimSpace(key))
}

func replayTrackKey(rec replayNDJSONRecord) string {
	if rec.Acid != "" {
		return "acid:" + strings.ToUpper(strings.TrimSpace(rec.Acid))
	}
	if acid, ok := replayExtractedString(rec.Extracted, "acid"); ok && acid != "" {
		return "acid:" + strings.ToUpper(strings.TrimSpace(acid))
	}
	if trackNum, ok := replayExtractedString(rec.Extracted, "trackNum"); ok && trackNum != "" {
		return "trk:" + strings.TrimSpace(trackNum)
	}
	if rec.JMSMessageID != "" {
		return "msg:" + rec.JMSMessageID
	}
	return ""
}

func replayRecordIsDelete(rec replayNDJSONRecord) bool {
	if strings.EqualFold(strings.TrimSpace(rec.MessageType), "D") {
		return true
	}
	if del, ok := replayExtractedString(rec.Extracted, "delete"); ok && strings.TrimSpace(del) == "1" {
		return true
	}
	if status, ok := replayExtractedString(rec.Extracted, "status"); ok {
		s := strings.ToLower(strings.TrimSpace(status))
		if s == "dropped" || s == "terminated" || s == "closed" {
			return true
		}
	}
	return false
}

func replayRecordLatitude(rec replayNDJSONRecord) (float32, bool) {
	if rec.Latitude != nil {
		return float32(*rec.Latitude), true
	}
	if v, ok := replayExtractedFloat(rec.Extracted, "lat"); ok {
		return float32(v), true
	}
	return 0, false
}

func replayRecordLongitude(rec replayNDJSONRecord) (float32, bool) {
	if rec.Longitude != nil {
		return float32(*rec.Longitude), true
	}
	if v, ok := replayExtractedFloat(rec.Extracted, "lon"); ok {
		return float32(v), true
	}
	return 0, false
}

func replayRecordAltitude(rec replayNDJSONRecord) float32 {
	if rec.AltitudeFt != nil {
		return float32(*rec.AltitudeFt)
	}
	if v, ok := replayExtractedFloat(rec.Extracted, "reportedAltitude"); ok {
		return float32(v)
	}
	if v, ok := replayExtractedFloat(rec.Extracted, "assignedAltitude"); ok {
		return float32(v)
	}
	return 0
}

func replayRecordCPS(rec replayNDJSONRecord) string {
	if rec.CPS != "" {
		return strings.ToUpper(strings.TrimSpace(rec.CPS))
	}
	if v, ok := replayExtractedString(rec.Extracted, "cps"); ok && v != "" {
		return strings.ToUpper(strings.TrimSpace(v))
	}
	return ""
}

// replayRecordSquawk returns the actual transponder code the aircraft is squawking.
// We prefer the reported beacon code (what the transponder is actually sending)
// over the assigned code.
func replayRecordSquawk(rec replayNDJSONRecord) (av.Squawk, bool) {
	for _, field := range []string{"reportedBeaconCode", "assignedBeaconCode"} {
		if raw, ok := replayExtractedString(rec.Extracted, field); ok {
			if sq, err := av.ParseSquawk(strings.TrimSpace(raw)); err == nil {
				return sq, true
			}
		}
	}
	if rec.BeaconCode != "" {
		if sq, err := av.ParseSquawk(strings.TrimSpace(rec.BeaconCode)); err == nil {
			return sq, true
		}
	}
	return 0, false
}

func replayRecordAcType(rec replayNDJSONRecord) string {
	if rec.AircraftType != "" {
		return strings.ToUpper(strings.TrimSpace(rec.AircraftType))
	}
	if v, ok := replayExtractedString(rec.Extracted, "acType"); ok && v != "" {
		return strings.ToUpper(strings.TrimSpace(v))
	}
	return ""
}

func replayRecordAssignedAlt(rec replayNDJSONRecord) float32 {
	if rec.AssignedAltitudeFt != nil {
		return float32(*rec.AssignedAltitudeFt)
	}
	if v, ok := replayExtractedFloat(rec.Extracted, "assignedAltitude"); ok {
		return float32(v)
	}
	return 0
}

func replayRecordScratchpad1(rec replayNDJSONRecord) string {
	if v, ok := replayExtractedString(rec.Extracted, "scratchPad1"); ok && v != "" {
		return strings.ToUpper(strings.TrimSpace(v))
	}
	return ""
}

func replayRecordScratchpad2(rec replayNDJSONRecord) string {
	if rec.SecondaryScratchpad != "" {
		return strings.ToUpper(strings.TrimSpace(rec.SecondaryScratchpad))
	}
	if v, ok := replayExtractedString(rec.Extracted, "scratchPad2"); ok && v != "" {
		return strings.ToUpper(strings.TrimSpace(v))
	}
	return ""
}

func replayRecordEntryFix(rec replayNDJSONRecord) string {
	if v, ok := replayExtractedString(rec.Extracted, "entryFix"); ok && v != "" {
		return strings.ToUpper(strings.TrimSpace(v))
	}
	return ""
}

func replayRecordExitFix(rec replayNDJSONRecord) string {
	if v, ok := replayExtractedString(rec.Extracted, "exitFix"); ok && v != "" {
		return strings.ToUpper(strings.TrimSpace(v))
	}
	return ""
}

func replayRecordDepAirport(rec replayNDJSONRecord) string {
	if v, ok := replayExtractedString(rec.Extracted, "departureAirport"); ok && v != "" {
		return strings.ToUpper(strings.TrimSpace(v))
	}
	return ""
}

func replayRecordDestAirport(rec replayNDJSONRecord) string {
	if v, ok := replayExtractedString(rec.Extracted, "destinationAirport"); ok && v != "" {
		return strings.ToUpper(strings.TrimSpace(v))
	}
	return ""
}

func replayRecordFlightRules(rec replayNDJSONRecord) av.FlightRules {
	rules := ""
	if v, ok := replayExtractedString(rec.Extracted, "flightRules"); ok {
		rules = strings.ToUpper(strings.TrimSpace(v))
	}
	switch rules {
	case "IFR":
		return av.FlightRulesIFR
	case "VFR", "E", "DVFR", "SVFR":
		return av.FlightRulesVFR
	default:
		return av.FlightRulesIFR // assume IFR if unknown
	}
}

func replayExtractedString(m map[string]any, key string) (string, bool) {
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func replayExtractedFloat(m map[string]any, key string) (float64, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case json.Number:
		f, err := val.Float64()
		if err == nil {
			return f, true
		}
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

func (r *realWorldReplay) StartTimestamp() float64 {
	return r.start
}

func (r *realWorldReplay) EndTimestamp() float64 {
	return r.end
}

func (r *realWorldReplay) TracksAtSimTime(simTime Time) map[av.ADSBCallsign]*Track {
	if len(r.events) == 0 {
		return nil
	}
	if !r.simStartSet {
		r.simStart = simTime
		r.simStartSet = true
	}

	targetTS := r.start + simTime.Sub(r.simStart).Seconds()
	if targetTS < r.start {
		targetTS = r.start
	}
	if targetTS > r.end {
		targetTS = r.end
	}

	for r.cursor < len(r.events) && r.events[r.cursor].timestamp <= targetTS {
		e := r.events[r.cursor]
		if e.delete {
			delete(r.active, e.key)
		} else {
			r.active[e.key] = e.state
		}
		r.cursor++
	}

	tracks := make(map[av.ADSBCallsign]*Track, len(r.active))
	for _, state := range r.active {
		trk := &Track{
			RadarTrack: av.RadarTrack{
				ADSBCallsign:        state.callsign,
				Squawk:              state.squawk,
				Mode:                av.TransponderModeAltitude,
				TrueAltitude:        state.altitude,
				TransponderAltitude: state.altitude,
				Location:            math.Point2LL{state.lon, state.lat},
			},
			CPS: state.cps,
		}

		if state.hasFlightPlan {
			trk.FlightPlan = &NASFlightPlan{
				ACID:                ACID(state.callsign),
				AircraftType:        state.acType,
				AssignedAltitude:    int(state.assignedAlt),
				Scratchpad:          state.scratchpad,
				SecondaryScratchpad: state.scratchpad2,
				EntryFix:            state.entryFix,
				ExitFix:             state.exitFix,
				ArrivalAirport:      state.destAirport,
				Rules:               state.rules,
				AssignedSquawk:      state.squawk,
			}
		} else {
			trk.MissingFlightPlan = true
		}

		tracks[state.callsign] = trk
	}

	return tracks
}
