package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// A variant of gateways alone is not a lab. The SQL function that tells the catalog and the events whether a task has
// infrastructure and the Go rule (HasInfrastructure, Topology.HasLab) are run on the same variants and must agree.
func TestGatewaysAloneAreNotInfrastructure_SQLAndGoAgree(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	node := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeNode}
	untyped := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "legacy"}
	hub := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "hub", Type: exerciseModel.DeviceTypeHub}
	vpn := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "attack", Type: exerciseModel.DeviceTypeVPNGateway}
	out := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "out", Type: exerciseModel.DeviceTypeInternetGateway}
	variant := func(devices ...exerciseModel.Device) exerciseModel.Variant {
		return exerciseModel.Variant{ID: uuid.Must(uuid.NewV7()), Topology: exerciseModel.Topology{Devices: devices}}
	}
	for name, variants := range map[string][]exerciseModel.Variant{
		"no devices":                   {variant()},
		"a vpn gateway only":           {variant(vpn)},
		"an internet gateway only":     {variant(out)},
		"both gateways, no node":       {variant(vpn, out)},
		"a node":                       {variant(node)},
		"a node and gateways":          {variant(vpn, node, out)},
		"a hub and a gateway":          {variant(vpn, hub)},
		"a device without a type":      {variant(untyped)},
		"gateways first, a node later": {variant(vpn), variant(out, node)},
		"gateways in every variant":    {variant(vpn), variant(out)},
	} {
		raw, err := json.Marshal(variants)
		if err != nil {
			t.Fatal(err)
		}
		var sql bool
		if err := db.Pool.QueryRow(ctx, `SELECT exercise_variants_have_infrastructure($1::jsonb)`, string(raw)).Scan(&sql); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if goRule := exerciseModel.HasInfrastructure(variants); sql != goRule {
			t.Errorf("%s: SQL says %v, Go says %v", name, sql, goRule)
		}
	}
	// the two settled meanings, not only their agreement
	var gatewaysOnly bool
	if err := db.Pool.QueryRow(ctx, `SELECT exercise_variants_have_infrastructure('[{"topology":{"devices":[{"name":"a","type":"vpn_gateway"},{"name":"b","type":"internet_gateway"}]}}]'::jsonb)`).Scan(&gatewaysOnly); err != nil || gatewaysOnly {
		t.Fatalf("gateways alone are not a lab: %v %v", gatewaysOnly, err)
	}
}

// A task offers VPN access when its text has an address in the network of a VPN gateway. A placeholder names its gateway only,
// so a placeholder of an Internet gateway must not count, however it is written.
func TestTaskOffersVPNAccess_OnlyAVPNGatewayCounts(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	taskID, otherTask := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	variants := func(tasks ...uuid.UUID) string {
		ids := ""
		for i, id := range tasks {
			if i > 0 {
				ids += ","
			}
			ids += `{"id":"` + id.String() + `"}`
		}
		return `[{"tasks":[` + ids + `],"topology":{"devices":[` +
			`{"name":"attack","type":"vpn_gateway"},{"name":"out","type":"internet_gateway"},{"name":"web","type":"node"}]}}]`
	}
	vpnSubnet := `{"kind":"gateway.subnet","gateway":"attack"}`
	vpnIP := `{"kind":"ip","ip_reference":"gateway","gateway":"attack","last_octet":10}`
	netSubnet := `{"kind":"gateway.subnet","gateway":"out"}`
	netIP := `{"kind":"ip","ip_reference":"gateway","gateway":"out","last_octet":10}`
	link := `{"kind":"external.link","device_name":"web"}`
	static := `{"kind":"ip","ip_reference":"static","octets_1to3":"10.0.0","last_octet":5}`
	snapshot := func(placeholders ...string) string {
		out := ""
		for i, p := range placeholders {
			if i > 0 {
				out += ","
			}
			out += p
		}
		return `{"name":"t","placeholders":[` + out + `]}`
	}
	for name, tc := range map[string]struct {
		snapshot string
		variants string
		task     uuid.UUID
		want     bool
	}{
		"vpn subnet":                           {snapshot(vpnSubnet), variants(taskID), taskID, true},
		"ip in the vpn network":                {snapshot(vpnIP), variants(taskID), taskID, true},
		"internet subnet":                      {snapshot(netSubnet), variants(taskID), taskID, false},
		"ip in the internet network":           {snapshot(netIP), variants(taskID), taskID, false},
		"a vpn and an internet placeholder":    {snapshot(vpnSubnet, netSubnet, netIP), variants(taskID), taskID, true},
		"the internet ones and a proxy link":   {snapshot(netSubnet, netIP, link), variants(taskID), taskID, false},
		"a static address and a link":          {snapshot(static, link), variants(taskID), taskID, false},
		"no placeholders":                      {`{"name":"t"}`, variants(taskID), taskID, false},
		"a gateway the variant does not have":  {snapshot(`{"kind":"gateway.subnet","gateway":"ghost"}`), variants(taskID), taskID, false},
		"the name of a node":                   {snapshot(`{"kind":"gateway.subnet","gateway":"web"}`), variants(taskID), taskID, false},
		"the task is not in the variants":      {snapshot(vpnSubnet), variants(otherTask), taskID, false},
		"no variants (the version is missing)": {snapshot(vpnSubnet), `null`, taskID, false},
		"placeholders that are not a list":     {`{"placeholders":null}`, variants(taskID), taskID, false},
	} {
		var got bool
		err := db.Pool.QueryRow(ctx, `SELECT task_offers_vpn_access($1::jsonb, $2::jsonb, $3::uuid)`, tc.snapshot, tc.variants, tc.task).Scan(&got)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != tc.want {
			t.Errorf("%s: VPN access = %v, want %v", name, got, tc.want)
		}
	}
	// the query that reports it runs against the real schema (the join to the pinned version)
	if _, err := db.Queries.ListEventIntegritySolves(ctx, uuid.Must(uuid.NewV7())); err != nil {
		t.Fatalf("ListEventIntegritySolves: %v", err)
	}
}
