package amt

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"math/bits"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// System Defense (issue #28): network filters (AMT_IPHeadersFilter, AMT_Hdr8021Filter), the
// policies that bundle them (AMT_SystemDefensePolicy) and the policy enabled on the wired port
// (AMT_NetworkPortSystemDefensePolicy). The filters act on the traffic of the host's operating
// system, not on Intel AMT's own (Intel AMT SDK, System Defense), so the device stays reachable
// under a policy that drops everything.
//
// AMT has no Put for these classes: changing a filter or a policy is deleting and creating it
// again. The library models none of them; the requests follow MeshCommander (AddDefenseFilter,
// AddDefensePolicy, changeDefaultPolicy). Intel documents the classes for AMT 3.0 to 11.0; a
// firmware without them answers the Enumerate with a fault and SystemDefense.Offered is false.

const (
	classDefensePolicy = "AMT_SystemDefensePolicy"
	classIPFilter      = "AMT_IPHeadersFilter"
	classEthFilter     = "AMT_Hdr8021Filter"
	classPortPolicy    = "AMT_NetworkPortSystemDefensePolicy"
	classFilterStats   = "AMT_ActiveFilterStatistics"
	classEthernetPort  = "CIM_EthernetPort"

	// wiredPortID is the DeviceID of the wired port's CIM_EthernetPort.
	wiredPortID = "Intel(r) AMT Ethernet Port 0"
	amtSystem   = "Intel(r) AMT"
	cimSystem   = "CIM_ComputerSystem"

	// MaxDefenseName is the longest filter or policy name (Name, PolicyName: MaxLen 16).
	MaxDefenseName = 16
	// MaxPolicyFilters is the size of FilterCreationHandles.
	MaxPolicyFilters = 62
)

// IP protocol numbers and EtherTypes of the filters.
const (
	ProtocolAny    = 0
	ProtocolICMP   = 1
	ProtocolTCP    = 6
	ProtocolUDP    = 17
	ProtocolICMPv6 = 58

	EtherTypeIP  = 0x0800
	EtherTypeARP = 0x0806
)

// FilterKind is the traffic a filter looks at.
type FilterKind int

const (
	FilterIPv4     FilterKind = iota + 1 // AMT_IPHeadersFilter, HdrIPVersion 4
	FilterIPv6                           // AMT_IPHeadersFilter, HdrIPVersion 6
	FilterEthernet                       // AMT_Hdr8021Filter by EtherType
	FilterIP                             // AMT_IPHeadersFilter without HdrIPVersion (both); only read
)

// FilterDirection is the direction of the traffic of the host.
type FilterDirection int

const (
	Inbound  FilterDirection = iota + 1 // FilterDirection 1, receive
	Outbound                            // FilterDirection 0, transmit
)

// FilterProfile values of AMT.
const (
	profileAllowCount = 0
	profileDropCount  = 1
	profileRateLimit  = 2
	profileAllow      = 3
	profileDrop       = 4
)

var (
	// ErrDefenseNameExists: a filter or policy with this name exists.
	ErrDefenseNameExists = errors.New("a filter or policy with this name exists")
	// ErrDefensePolicyActive: the policy is enabled on the port; turn it off before deleting it.
	ErrDefensePolicyActive = errors.New("the policy is active")
	// ErrDefenseNotFound: the device has no filter or policy with this ID (any more).
	ErrDefenseNotFound = errors.New("the filter or policy does not exist")
)

// DefenseFilterInUseError: a policy contains the filter, so it cannot be deleted.
type DefenseFilterInUseError struct{ Policy string }

func (e *DefenseFilterInUseError) Error() string {
	return fmt.Sprintf("the policy %q uses this filter", e.Policy)
}

// DefenseFilter is one System Defense filter.
type DefenseFilter struct {
	ID        string // InstanceID, the creation handle a policy lists
	Name      string
	Kind      FilterKind
	Direction FilterDirection
	// Protocol is the IP protocol (ProtocolAny for all) or, for FilterEthernet, the EtherType.
	Protocol int
	// PortStart and PortEnd are the destination ports (TCP and UDP); 0 and 0 match all.
	PortStart, PortEnd int
	// Remote is the address of the other side: the source of inbound and the destination of
	// outbound traffic. Not valid: any address.
	Remote    netip.Prefix
	Drop      bool // matching packets are dropped
	Count     bool // matches are counted
	RateLimit int  // packets per second of a rate limit filter (FilterProfile 2); 0 otherwise
	Event     bool // an event log entry on match

	// Matches is the count of the active policy's statistics; HasMatches is false when the
	// device reported none for the filter.
	Matches    int
	HasMatches bool

	class string
}

// Drops reports whether the filter drops packets (drop, or rate limit).
func (f DefenseFilter) Drops() bool { return f.Drop || f.RateLimit > 0 }

// DefensePolicy is one System Defense policy.
type DefensePolicy struct {
	ID         string // InstanceID, "Intel(r) AMT:Handle:1"
	Name       string
	Precedence int // the higher one takes effect when several are active
	// DropIn and DropOut drop the inbound and outbound traffic no filter matches.
	DropIn, DropOut bool
	Filters         []string // IDs of its filters
	Active          bool     // enabled on the wired port
}

// SystemDefense is what the System Defense page shows.
type SystemDefense struct {
	// Offered is false when the device has no AMT_SystemDefensePolicy (AMT 12 and later).
	Offered  bool
	Policies []DefensePolicy
	Filters  []DefenseFilter
	// Active is the ID of the policy enabled on the wired port; "" for none.
	Active string
}

// Policy returns the policy with id.
func (s SystemDefense) Policy(id string) (DefensePolicy, bool) {
	for _, p := range s.Policies {
		if p.ID == id {
			return p, true
		}
	}
	return DefensePolicy{}, false
}

// Filter returns the filter with id.
func (s SystemDefense) Filter(id string) (DefenseFilter, bool) {
	for _, f := range s.Filters {
		if f.ID == id {
			return f, true
		}
	}
	return DefenseFilter{}, false
}

// Drops reports whether the policy id drops traffic: by default in a direction, or through a
// filter that drops or limits. This is what OpDefenseDrop confirms when the policy is
// activated, also by an agent watchdog.
func (s SystemDefense) Drops(id string) bool {
	p, ok := s.Policy(id)
	if !ok {
		return false
	}
	if p.DropIn || p.DropOut {
		return true
	}
	for _, fid := range p.Filters {
		if f, ok := s.Filter(fid); ok && f.Drops() {
			return true
		}
	}
	return false
}

// FilterSpec is a new filter.
type FilterSpec struct {
	Name      string
	Kind      FilterKind // FilterIPv4, FilterIPv6 or FilterEthernet
	Direction FilterDirection
	// Protocol: IP protocol (ProtocolAny, ProtocolTCP, ProtocolUDP, ProtocolICMP, ProtocolICMPv6)
	// or the EtherType for FilterEthernet (EtherTypeIP, EtherTypeARP or another of at least 0x0600).
	Protocol           int
	PortStart, PortEnd int          // destination ports, TCP and UDP only; 0 and 0: all
	Remote             netip.Prefix // not valid: any address; IP kinds only, of the kind's family
	Drop, Count, Event bool
}

func checkDefenseName(name string) error {
	switch {
	case name == "":
		return invalid("Name", "must not be empty")
	case utf8.RuneCountInString(name) > MaxDefenseName:
		return invalid("Name", fmt.Sprintf("at most %d characters", MaxDefenseName))
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return invalid("Name", "must not contain control characters")
	}
	return nil
}

// Validate checks the spec without device access; it returns a *ValidationError.
func (s FilterSpec) Validate() error {
	if err := checkDefenseName(strings.TrimSpace(s.Name)); err != nil {
		return err
	}
	if s.Direction != Inbound && s.Direction != Outbound {
		return invalid("Direction", "inbound or outbound")
	}
	switch s.Kind {
	case FilterEthernet:
		if s.Protocol < 0x0600 || s.Protocol > 0xFFFF {
			return invalid("Protocol", "an EtherType from 0x0600")
		}
		if s.PortStart != 0 || s.PortEnd != 0 || s.Remote.IsValid() {
			return invalid("Protocol", "an Ethernet filter has no ports or addresses")
		}
		return nil
	case FilterIPv4, FilterIPv6:
	default:
		return invalid("Kind", "IPv4, IPv6 or Ethernet")
	}
	if s.Protocol < 0 || s.Protocol > 255 {
		return invalid("Protocol", "0 to 255")
	}
	if s.PortStart != 0 || s.PortEnd != 0 {
		switch {
		case s.Protocol != ProtocolTCP && s.Protocol != ProtocolUDP:
			return invalid("Ports", "only for TCP and UDP")
		case s.PortStart < 1 || s.PortEnd > 65535 || s.PortStart > s.PortEnd:
			return invalid("Ports", "1 to 65535, the first not above the last")
		}
	}
	if s.Remote.IsValid() {
		if s.Remote.Addr().Is4() != (s.Kind == FilterIPv4) || s.Remote.Addr().Zone() != "" {
			return invalid("Remote", "an address of the filter's IP version")
		}
	}
	return nil
}

// PolicySpec is a new policy.
type PolicySpec struct {
	Name       string
	Precedence int      // 0 to 4294967295; the higher one takes effect
	Filters    []string // filter IDs, at most MaxPolicyFilters
	Drop       bool     // traffic no filter matches is dropped (both directions)
}

// Validate checks the spec without device access; it returns a *ValidationError.
func (s PolicySpec) Validate() error {
	if err := checkDefenseName(strings.TrimSpace(s.Name)); err != nil {
		return err
	}
	if s.Precedence < 0 || int64(s.Precedence) > math.MaxUint32 {
		return invalid("Precedence", "0 to 4294967295")
	}
	if len(s.Filters) > MaxPolicyFilters {
		return invalid("Filters", fmt.Sprintf("at most %d filters", MaxPolicyFilters))
	}
	return nil
}

// addrBytes reads an OctetString address property: hex (as MeshCommander writes it), base64,
// or one decimal byte per element. Missing or unreadable: nil.
func addrBytes(values []string) []byte {
	switch {
	case len(values) == 1:
		v := strings.TrimSpace(values[0])
		if b, err := hex.DecodeString(v); err == nil && (len(b) == 4 || len(b) == 16 || len(b) == 20) {
			return b
		}
		if b, err := base64.StdEncoding.DecodeString(v); err == nil && (len(b) == 4 || len(b) == 16 || len(b) == 20) {
			return b
		}
		if a, err := netip.ParseAddr(v); err == nil {
			return a.AsSlice()
		}
	case len(values) == 4 || len(values) == 16 || len(values) == 20:
		b := make([]byte, 0, len(values))
		for _, v := range values {
			n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 8)
			if err != nil {
				return nil
			}
			b = append(b, byte(n))
		}
		return b
	}
	return nil
}

// prefixOf combines address and mask values into a prefix; a missing mask is a host route.
func prefixOf(addr, mask []string) netip.Prefix {
	ab := addrBytes(addr)
	if ab == nil {
		return netip.Prefix{}
	}
	if len(ab) == 20 {
		ab = ab[:16] // with the scope identifier
	}
	a, ok := netip.AddrFromSlice(ab)
	if !ok {
		return netip.Prefix{}
	}
	n := a.BitLen()
	if mb := addrBytes(mask); mb != nil {
		n = 0
		for _, b := range mb[:min(len(mb), len(ab))] {
			n += bits.OnesCount8(b)
		}
	}
	p, err := a.Prefix(n)
	if err != nil {
		return netip.Prefix{}
	}
	return p
}

// maskHex is the netmask of p as uppercase hex.
func maskHex(p netip.Prefix) string {
	b := make([]byte, p.Addr().BitLen()/8)
	for i := range p.Bits() {
		b[i/8] |= 0x80 >> (i % 8)
	}
	return strings.ToUpper(hex.EncodeToString(b))
}

func filterFrom(o *cimObject) DefenseFilter {
	f := DefenseFilter{ID: o.str("InstanceID"), Name: o.str("Name"), class: o.class, Event: o.boolean("ActionEventOnMatch")}
	if o.integer("FilterDirection") == 1 {
		f.Direction = Inbound
	} else {
		f.Direction = Outbound
	}
	switch o.integer("FilterProfile") {
	case profileAllowCount:
		f.Count = true
	case profileDropCount:
		f.Drop, f.Count = true, true
	case profileRateLimit:
		f.RateLimit = max(1, o.integer("FilterProfileData"))
	case profileDrop:
		f.Drop = true
	}
	if o.class == classEthFilter {
		f.Kind, f.Protocol = FilterEthernet, o.integer("HdrProtocolID8021")
		return f
	}
	switch o.integer("HdrIPVersion") {
	case 4:
		f.Kind = FilterIPv4
	case 6:
		f.Kind = FilterIPv6
	default:
		f.Kind = FilterIP
	}
	f.Protocol = o.integer("HdrProtocolID")
	if o.has("HdrDestPortStart") || o.has("HdrDestPortEnd") {
		f.PortStart, f.PortEnd = o.integer("HdrDestPortStart"), 65535
		if o.has("HdrDestPortEnd") {
			f.PortEnd = o.integer("HdrDestPortEnd")
		}
	}
	if f.Direction == Inbound {
		f.Remote = prefixOf(o.strs("HdrSrcAddress"), o.strs("HdrSrcMask"))
	} else {
		f.Remote = prefixOf(o.strs("HdrDestAddress"), o.strs("HdrDestMask"))
	}
	return f
}

func policyFrom(o *cimObject) DefensePolicy {
	p := DefensePolicy{ID: o.str("InstanceID"), Name: o.str("PolicyName"), Precedence: o.integer("PolicyPrecedence"),
		DropIn: o.boolean("RxDefaultDrop"), DropOut: o.boolean("TxDefaultDrop")}
	for _, h := range o.strs("FilterCreationHandles") {
		if h != "" {
			p.Filters = append(p.Filters, h)
		}
	}
	if p.Name == "" {
		p.Name = o.str("ElementName")
	}
	return p
}

// GetSystemDefense reads policies, filters, the policy enabled on the wired port and, while one
// is, its filter statistics. A firmware without AMT_SystemDefensePolicy yields Offered false
// and no error; a missing filter class leaves its filters out. The statistics are optional.
func (d *Device) GetSystemDefense() (SystemDefense, error) { return d.systemDefense(true) }

// systemDefense is GetSystemDefense; stats false skips the statistics (before a write).
func (d *Device) systemDefense(stats bool) (SystemDefense, error) {
	const op = "read system defense"
	objs, offered, err := d.optionalClass(classDefensePolicy)
	if err != nil {
		return SystemDefense{}, writeError(op, err)
	}
	if !offered {
		return SystemDefense{}, nil
	}
	s := SystemDefense{Offered: true}
	for _, o := range objs {
		s.Policies = append(s.Policies, policyFrom(o))
	}
	for _, class := range []string{classIPFilter, classEthFilter} {
		objs, _, err := d.optionalClass(class)
		if err != nil {
			return SystemDefense{}, writeError(op, err)
		}
		for _, o := range objs {
			s.Filters = append(s.Filters, filterFrom(o))
		}
	}
	links, err := d.pullLinks(classPortPolicy)
	if err != nil && !isMissingClass(err) {
		return SystemDefense{}, writeError(op, err)
	}
	for _, l := range links {
		if l.Antecedent.selector("DeviceID") == wiredPortID {
			s.Active = l.Dependent.selector("InstanceID")
		}
	}
	for i := range s.Policies {
		s.Policies[i].Active = s.Policies[i].ID == s.Active && s.Active != ""
	}
	slices.SortStableFunc(s.Policies, func(a, b DefensePolicy) int { return b.Precedence - a.Precedence })
	if stats && s.Active != "" {
		d.readFilterStats(&s)
	}
	return s, nil
}

// readFilterStats updates the statistics of the active policy on the wired port
// (UpdateStatistics) and reads them (AMT_ActiveFilterStatistics) into the filters. Failures
// leave the counts out.
func (d *Device) readFilterStats(s *SystemDefense) {
	nic := `<a:Address></a:Address><a:ReferenceParameters><w:ResourceURI>` + resourceURI(classEthernetPort) +
		`</w:ResourceURI><w:SelectorSet><w:Selector Name="DeviceID">` + wiredPortID + `</w:Selector></w:SelectorSet></a:ReferenceParameters>`
	if err := d.invokeRaw(classDefensePolicy, "UpdateStatistics", []selector{{"InstanceID", s.Active}},
		`<h:NetworkInterface>`+nic+`</h:NetworkInterface><h:ResetOnRead>false</h:ResetOnRead>`); err != nil {
		return
	}
	pulls, err := d.pullRaw(classFilterStats)
	if err != nil {
		return
	}
	counts := map[string]int{}
	for _, p := range pulls {
		for _, st := range parseFilterStats(p) {
			if st.port == wiredPortID && st.filter != "" {
				counts[st.filter] = st.count
			}
		}
	}
	for i, f := range s.Filters {
		if n, ok := counts[f.Name]; ok {
			s.Filters[i].Matches, s.Filters[i].HasMatches = n, true
		}
	}
}

type filterStat struct {
	port, filter string
	count        int
}

// parseFilterStats reads AMT_ActiveFilterStatistics items: the port of the Antecedent, the
// filter name from the nested reference inside the Dependent, and ReadCount.
func parseFilterStats(raw string) []filterStat {
	var body struct {
		Items []struct {
			ReadCount  string `xml:"ReadCount"`
			Antecedent eprXML `xml:"Antecedent"`
			Dependent  struct {
				Inner string `xml:",innerxml"`
			} `xml:"Dependent"`
		} `xml:"Body>PullResponse>Items>AMT_ActiveFilterStatistics"`
	}
	if xml.Unmarshal([]byte(raw), &body) != nil {
		return nil
	}
	var out []filterStat
	for _, it := range body.Items {
		n, _ := strconv.Atoi(strings.TrimSpace(it.ReadCount))
		out = append(out, filterStat{port: it.Antecedent.selector("DeviceID"), filter: nestedSelector(it.Dependent.Inner, "Name"), count: n})
	}
	return out
}

// nestedSelector returns the text of the innermost selector called name in an XML fragment.
func nestedSelector(fragment, name string) string {
	dec := xml.NewDecoder(strings.NewReader("<r>" + fragment + "</r>"))
	found := ""
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) || err != nil {
			return found
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "Selector" {
			continue
		}
		for _, a := range start.Attr {
			if a.Name.Local == "Name" && a.Value == name {
				var v string
				if dec.DecodeElement(&v, &start) == nil {
					found = strings.TrimSpace(v)
				}
			}
		}
	}
}

// nameTaken reports a filter or policy of s called name (any case).
func (s SystemDefense) nameTaken(name string, filters bool) bool {
	if filters {
		return slices.ContainsFunc(s.Filters, func(f DefenseFilter) bool { return strings.EqualFold(f.Name, name) })
	}
	return slices.ContainsFunc(s.Policies, func(p DefensePolicy) bool { return strings.EqualFold(p.Name, name) })
}

// AddDefenseFilter creates a filter (AMT_IPHeadersFilter or AMT_Hdr8021Filter). A name in use
// is ErrDefenseNameExists without a request.
func (d *Device) AddDefenseFilter(s FilterSpec) error {
	const op = "add system defense filter"
	if err := s.Validate(); err != nil {
		return err
	}
	name := strings.TrimSpace(s.Name)
	sd, err := d.systemDefense(false)
	if err != nil {
		return err
	}
	if !sd.Offered {
		return fmt.Errorf("%s: %w", op, ErrNotModifiable)
	}
	if sd.nameTaken(name, true) {
		return ErrDefenseNameExists
	}
	profile := profileAllow
	switch {
	case s.Drop && s.Count:
		profile = profileDropCount
	case s.Drop:
		profile = profileDrop
	case s.Count:
		profile = profileAllowCount
	}
	dir := "1"
	if s.Direction == Outbound {
		dir = "0"
	}
	class := classIPFilter
	if s.Kind == FilterEthernet {
		class = classEthFilter
	}
	var b strings.Builder
	b.WriteString(prop("InstanceID", "0") + prop("Name", name) + prop("CreationClassName", class) +
		prop("SystemName", amtSystem) + prop("SystemCreationClassName", cimSystem))
	if s.Kind == FilterEthernet {
		b.WriteString(prop("HdrProtocolID8021", strconv.Itoa(s.Protocol)))
	} else {
		ver := "4"
		if s.Kind == FilterIPv6 {
			ver = "6"
		}
		b.WriteString(prop("HdrIPVersion", ver))
		if s.Remote.IsValid() {
			p := s.Remote.Masked()
			addr, mask := strings.ToUpper(hex.EncodeToString(p.Addr().AsSlice())), maskHex(p)
			if s.Direction == Inbound {
				b.WriteString(prop("HdrSrcAddress", addr) + prop("HdrSrcMask", mask))
			} else {
				b.WriteString(prop("HdrDestAddress", addr) + prop("HdrDestMask", mask))
			}
		}
		if s.Protocol != ProtocolAny {
			b.WriteString(prop("HdrProtocolID", strconv.Itoa(s.Protocol)))
		}
		if s.PortStart != 0 {
			b.WriteString(prop("HdrDestPortStart", strconv.Itoa(s.PortStart)) + prop("HdrDestPortEnd", strconv.Itoa(s.PortEnd)))
		}
	}
	b.WriteString(prop("FilterProfile", strconv.Itoa(profile)) + prop("FilterDirection", dir) +
		prop("ActionEventOnMatch", strconv.FormatBool(s.Event)))
	return writeError(op, d.createRaw(class, b.String()))
}

// DeleteDefenseFilter deletes the filter id. A filter a policy contains is refused with
// *DefenseFilterInUseError before any write. The UI asks first (OpDefenseDelete).
func (d *Device) DeleteDefenseFilter(id string) error {
	const op = "delete system defense filter"
	sd, err := d.systemDefense(false)
	if err != nil {
		return err
	}
	f, ok := sd.Filter(id)
	if !ok {
		return ErrDefenseNotFound
	}
	for _, p := range sd.Policies {
		if slices.Contains(p.Filters, id) {
			return &DefenseFilterInUseError{Policy: p.Name}
		}
	}
	return writeError(op, d.deleteRaw(f.class, []selector{{"InstanceID", f.ID}}))
}

// AddDefensePolicy creates a policy with the filters of s; the traffic no filter matches is
// allowed or dropped in both directions. Unknown filter IDs are a *ValidationError, a name in
// use ErrDefenseNameExists, both without a request.
func (d *Device) AddDefensePolicy(s PolicySpec) error {
	const op = "add system defense policy"
	if err := s.Validate(); err != nil {
		return err
	}
	name := strings.TrimSpace(s.Name)
	sd, err := d.systemDefense(false)
	if err != nil {
		return err
	}
	if !sd.Offered {
		return fmt.Errorf("%s: %w", op, ErrNotModifiable)
	}
	if sd.nameTaken(name, false) {
		return ErrDefenseNameExists
	}
	for _, id := range s.Filters {
		if _, ok := sd.Filter(id); !ok {
			return invalid("Filters", "the device has no such filter")
		}
	}
	drop := strconv.FormatBool(s.Drop)
	var b strings.Builder
	// The order of MeshCommander's AddDefensePolicyOk.
	b.WriteString(prop("InstanceID", "0") + prop("PolicyName", name) + prop("PolicyPrecedence", strconv.Itoa(s.Precedence)) +
		prop("TxDefaultCount", "false") + prop("TxDefaultDrop", drop) + prop("TxDefaultMatchEvent", "false") +
		prop("RxDefaultCount", "false") + prop("RxDefaultDrop", drop) + prop("RxDefaultMatchEvent", "false"))
	for _, id := range s.Filters {
		b.WriteString(prop("FilterCreationHandles", id))
	}
	return writeError(op, d.createRaw(classDefensePolicy, b.String()))
}

// DeleteDefensePolicy deletes the policy id. The active policy is refused with
// ErrDefensePolicyActive. The UI asks first (OpDefenseDelete).
func (d *Device) DeleteDefensePolicy(id string) error {
	const op = "delete system defense policy"
	sd, err := d.systemDefense(false)
	if err != nil {
		return err
	}
	p, ok := sd.Policy(id)
	if !ok {
		return ErrDefenseNotFound
	}
	if p.Active {
		return ErrDefensePolicyActive
	}
	return writeError(op, d.deleteRaw(classDefensePolicy, []selector{{"InstanceID", p.ID}}))
}

// ActivateDefensePolicy enables the policy id on the wired port in place of the active one
// (delete the old link, create the new one, as MeshCommander). A policy that drops traffic
// (SystemDefense.Drops) needs confirm for OpDefenseDrop.
func (d *Device) ActivateDefensePolicy(id string, confirm Confirmation) error {
	const op = "activate system defense policy"
	sd, err := d.systemDefense(false)
	if err != nil {
		return err
	}
	if _, ok := sd.Policy(id); !ok {
		return ErrDefenseNotFound
	}
	if sd.Active == id {
		return nil
	}
	if sd.Drops(id) {
		if err := d.confirmed(confirm, OpDefenseDrop, "Policy"); err != nil {
			return err
		}
	}
	if sd.Active != "" {
		if err := d.deletePortLink(classPortPolicy, sd.Active); err != nil {
			return writeError(op, err)
		}
	}
	return writeError(op, d.createPortLink(classPortPolicy, id))
}

// DeactivateDefensePolicy turns the active policy off: the wired port then has none. Without an
// active policy it does nothing.
func (d *Device) DeactivateDefensePolicy() error {
	const op = "turn off system defense policy"
	sd, err := d.systemDefense(false)
	if err != nil {
		return err
	}
	if sd.Active == "" {
		return nil
	}
	return writeError(op, d.deletePortLink(classPortPolicy, sd.Active))
}
