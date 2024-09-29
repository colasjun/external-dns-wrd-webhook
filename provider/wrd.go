package provider

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	nc "github.com/aellwein/netcup-dns-api/pkg/v1"
	"github.com/go-kit/log"
	"github.com/go-kit/log/level"

	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	"sigs.k8s.io/external-dns/provider"
)

var allEndpoints []*endpoint.Endpoint

// WrdProvider is an implementation of Provider for Wrd DNS.
type WrdProvider struct {
	provider.BaseProvider
	// client       *nc.NetcupDnsClient
	session      *nc.NetcupSession
	domainFilter endpoint.DomainFilter
	dryRun       int
	logger       log.Logger
}

// WrdChange includes the changesets that need to be applied to the Wrd API
type WrdChange struct {
	Create    *[]nc.DnsRecord
	UpdateNew *[]nc.DnsRecord
	UpdateOld *[]nc.DnsRecord
	Delete    *[]nc.DnsRecord
}

type DnsRecord struct {
	Id           string `json:"id"`
	Hostname     string `json:"hostname"`
	Type         string `json:"type"`
	Priority     string `json:"priority"`
	Destination  string `json:"destination"`
	DeleteRecord bool   `json:"deleterecord"`
	State        string `json:"state"`
}

// NewWrdProvider creates a new provider including the wrd API client
func NewWrdProvider(domainFilterList *[]string, dryRun int, logger log.Logger) (*WrdProvider, error) {
	domainFilter := endpoint.NewDomainFilter(*domainFilterList)

	if !domainFilter.IsConfigured() {
		return nil, fmt.Errorf("wrd provider requires at least one configured domain in the domainFilter")
	}

	/*if customerID == 0 {
		return nil, fmt.Errorf("netcup provider requires a customer ID")
	}

	if apiKey == "" {
		return nil, fmt.Errorf("netcup provider requires an API Key")
	}

	if apiPassword == "" {
		return nil, fmt.Errorf("netcup provider requires an API Password")
	}*/

	// client := nc.NewNetcupDnsClient(customerID, apiKey, apiPassword)

	return &WrdProvider{
		// client:       client,
		domainFilter: domainFilter,
		dryRun:       dryRun,
		logger:       logger,
	}, nil
}

// Records delivers the list of Endpoint records for all zones.
func (p *WrdProvider) Records(ctx context.Context) ([]*endpoint.Endpoint, error) {
	// todo 需要完善 和ddi对接

	endpoints := make([]*endpoint.Endpoint, 0)

	if p.dryRun > 0 {
		_ = level.Debug(p.logger).Log("msg", "dry run - skipping login")

		return allEndpoints, nil
	} else {
		err := p.ensureLogin()
		if err != nil {
			return nil, err
		}

		defer p.session.Logout() //nolint:errcheck

		for _, domain := range p.domainFilter.Filters {
			// some information is on DNS zone itself, query it first
			zone, err := p.session.InfoDnsZone(domain)
			if err != nil {
				return nil, fmt.Errorf("unable to query DNS zone info for domain '%v': %v", domain, err)
			}
			ttl, err := strconv.ParseUint(zone.Ttl, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("unexpected error: unable to convert '%s' to uint64", zone.Ttl)
			}
			// query the records of the domain
			recs, err := p.session.InfoDnsRecords(domain)
			if err != nil {
				if p.session.LastResponse != nil && p.session.LastResponse.Status == string(nc.StatusError) && p.session.LastResponse.StatusCode == 5029 {
					_ = level.Debug(p.logger).Log("msg", "no records exist", "domain", domain, "error", err)
				} else {
					return nil, fmt.Errorf("unable to get DNS records for domain '%v': %v", domain, err)
				}
			}
			_ = level.Info(p.logger).Log("msg", "got DNS records for domain", "domain", domain)
			for _, rec := range *recs {
				name := fmt.Sprintf("%s.%s", rec.Hostname, domain)
				if rec.Hostname == "@" {
					name = domain
				}

				ep := endpoint.NewEndpointWithTTL(name, rec.Type, endpoint.TTL(ttl), rec.Destination)
				endpoints = append(endpoints, ep)
			}
		}
	}
	for _, endpointItem := range endpoints {
		_ = level.Debug(p.logger).Log("msg", "endpoints collected", "endpoints", endpointItem.String())
	}

	return endpoints, nil
}

// ApplyChanges applies a given set of changes in a given zone.
func (p *WrdProvider) ApplyChanges(ctx context.Context, changes *plan.Changes) error {
	if !changes.HasChanges() {
		_ = level.Debug(p.logger).Log("msg", "no changes detected - nothing to do")
		return nil
	}

	allEndpoints = []*endpoint.Endpoint{}

	// todo 需要完善 和ddi对接
	fmt.Printf("changes:%+v \n", changes)

	if p.dryRun > 0 {
		_ = level.Debug(p.logger).Log("msg", "dry run - skipping login")
	} else {
		err := p.ensureLogin()
		if err != nil {
			return err
		}
		defer p.session.Logout() //nolint:errcheck
	}
	perZoneChanges := map[string]*plan.Changes{}

	for _, zoneName := range p.domainFilter.Filters {
		_ = level.Debug(p.logger).Log("msg", "zone detected", "zone", zoneName)

		perZoneChanges[zoneName] = &plan.Changes{}
	}

	for _, ep := range changes.Create {
		zoneName := endpointZoneName(ep, p.domainFilter.Filters)
		if zoneName == "" {
			_ = level.Debug(p.logger).Log("msg", "ignoring change since it did not match any zone", "type", "create", "endpoint", ep)
			continue
		}
		_ = level.Debug(p.logger).Log("msg", "planning", "type", "create", "endpoint", ep, "zone", zoneName)

		allEndpoints = append(allEndpoints, ep)

		perZoneChanges[zoneName].Create = append(perZoneChanges[zoneName].Create, ep)
	}

	for _, ep := range changes.UpdateOld {
		zoneName := endpointZoneName(ep, p.domainFilter.Filters)
		if zoneName == "" {
			_ = level.Debug(p.logger).Log("msg", "ignoring change since it did not match any zone", "type", "updateOld", "endpoint", ep)
			continue
		}
		_ = level.Debug(p.logger).Log("msg", "planning", "type", "updateOld", "endpoint", ep, "zone", zoneName)

		perZoneChanges[zoneName].UpdateOld = append(perZoneChanges[zoneName].UpdateOld, ep)
	}

	for _, ep := range changes.UpdateNew {
		zoneName := endpointZoneName(ep, p.domainFilter.Filters)
		if zoneName == "" {
			_ = level.Debug(p.logger).Log("msg", "ignoring change since it did not match any zone", "type", "updateNew", "endpoint", ep)
			continue
		}
		_ = level.Debug(p.logger).Log("msg", "planning", "type", "updateNew", "endpoint", ep, "zone", zoneName)
		perZoneChanges[zoneName].UpdateNew = append(perZoneChanges[zoneName].UpdateNew, ep)
	}

	for _, ep := range changes.Delete {
		zoneName := endpointZoneName(ep, p.domainFilter.Filters)
		if zoneName == "" {
			_ = level.Debug(p.logger).Log("msg", "ignoring change since it did not match any zone", "type", "delete", "endpoint", ep)
			continue
		}
		_ = level.Debug(p.logger).Log("msg", "planning", "type", "delete", "endpoint", ep, "zone", zoneName)
		perZoneChanges[zoneName].Delete = append(perZoneChanges[zoneName].Delete, ep)
	}

	if p.dryRun > 0 {
		_ = level.Info(p.logger).Log("msg", "dry run - not applying changes")
		return nil
	}

	// Assemble changes per zone and prepare it for the Netcup API client
	for zoneName, c := range perZoneChanges {
		// Gather records from API to extract the record ID which is necessary for updating/deleting the record
		recs, err := p.session.InfoDnsRecords(zoneName)
		if err != nil {
			if p.session.LastResponse != nil && p.session.LastResponse.Status == string(nc.StatusError) && p.session.LastResponse.StatusCode == 5029 {
				_ = level.Debug(p.logger).Log("msg", "no records exist", "zone", zoneName, "error", err)
			} else {
				_ = level.Error(p.logger).Log("msg", "unable to get DNS records for domain", "zone", zoneName, "error", err)
			}
		}
		change := &WrdChange{
			Create:    convertToNetcupRecord(recs, c.Create, zoneName, false),
			UpdateNew: convertToNetcupRecord(recs, c.UpdateNew, zoneName, false),
			UpdateOld: convertToNetcupRecord(recs, c.UpdateOld, zoneName, true),
			Delete:    convertToNetcupRecord(recs, c.Delete, zoneName, true),
		}

		// If not in dry run, apply changes
		_, err = p.session.UpdateDnsRecords(zoneName, change.UpdateOld)
		if err != nil {
			return err
		}
		_, err = p.session.UpdateDnsRecords(zoneName, change.Delete)
		if err != nil {
			return err
		}
		_, err = p.session.UpdateDnsRecords(zoneName, change.Create)
		if err != nil {
			return err
		}
		_, err = p.session.UpdateDnsRecords(zoneName, change.UpdateNew)
		if err != nil {
			return err
		}
	}

	_ = level.Debug(p.logger).Log("msg", "update completed")

	return nil
}

// convertToNetcupRecord transforms a list of endpoints into a list of Netcup DNS Records
// returns a pointer to a list of DNS Records
func convertToNetcupRecord(recs *[]nc.DnsRecord, endpoints []*endpoint.Endpoint, zoneName string, DeleteRecord bool) *[]nc.DnsRecord {
	records := make([]nc.DnsRecord, len(endpoints))

	for i, ep := range endpoints {
		recordName := strings.TrimSuffix(ep.DNSName, "."+zoneName)
		if recordName == zoneName {
			recordName = "@"
		}
		target := ep.Targets[0]
		if ep.RecordType == endpoint.RecordTypeTXT && strings.HasPrefix(target, "\"heritage=") {
			target = strings.Trim(ep.Targets[0], "\"")
		}

		records[i] = nc.DnsRecord{
			Type:         ep.RecordType,
			Hostname:     recordName,
			Destination:  target,
			Id:           getIDforRecord(recordName, target, ep.RecordType, recs),
			DeleteRecord: DeleteRecord,
		}
	}
	return &records
}

// getIDforRecord compares the endpoint with existing records to get the ID from Netcup to ensure it can be safely removed.
// returns empty string if no match found
func getIDforRecord(recordName string, target string, recordType string, recs *[]nc.DnsRecord) string {
	for _, rec := range *recs {
		if recordType == rec.Type && target == rec.Destination && rec.Hostname == recordName {
			return rec.Id
		}
	}

	return ""
}

// endpointZoneName determines zoneName for endpoint by taking longest suffix zoneName match in endpoint DNSName
// returns empty string if no match found
func endpointZoneName(endpoint *endpoint.Endpoint, zones []string) (zone string) {
	var matchZoneName string = ""
	for _, zoneName := range zones {
		if strings.HasSuffix(endpoint.DNSName, zoneName) && len(zoneName) > len(matchZoneName) {
			matchZoneName = zoneName
		}
	}
	return matchZoneName
}

// ensureLogin makes sure that we are logged in to Netcup API.
func (p *WrdProvider) ensureLogin() error {
	return nil
	/*_ = level.Debug(p.logger).Log("msg", "performing login to Netcup DNS API")
	session, err := p.client.Login()
	if err != nil {
		return err
	}
	p.session = session
	_ = level.Debug(p.logger).Log("msg", "successfully logged in to Netcup DNS API")
	return nil*/
}
