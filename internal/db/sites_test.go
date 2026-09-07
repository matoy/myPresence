package db

import (
	"testing"

	"github.com/matoy/mypresence/internal/models"
)

func TestSites_CRUDAndFloorplanAttachment(t *testing.T) {
	d := newTestDB(t)

	// 1. Create Floorplans
	fp1, err := d.CreateFloorplan("Floor 1", 0)
	if err != nil {
		t.Fatalf("CreateFloorplan 1 failed: %v", err)
	}
	fp2, err := d.CreateFloorplan("Floor 2", 1)
	if err != nil {
		t.Fatalf("CreateFloorplan 2 failed: %v", err)
	}

	// 2. Create Site with floorplans
	site1ID, err := d.CreateSite(models.Site{
		Name:             "HQ Paris",
		CountryCode:      "FR",
		NotCorporateSite: false,
		FloorplanIDs:     []int64{fp1},
	})
	if err != nil {
		t.Fatalf("CreateSite failed: %v", err)
	}
	if site1ID <= 0 {
		t.Fatalf("expected positive site ID, got %d", site1ID)
	}

	// 3. Create second site (non-corporate)
	site2ID, err := d.CreateSite(models.Site{
		Name:             "Coworking Casablanca",
		CountryCode:      "MA",
		NotCorporateSite: true,
		FloorplanIDs:     []int64{fp2},
	})
	if err != nil {
		t.Fatalf("CreateSite 2 failed: %v", err)
	}

	// 4. GetSite
	s1, err := d.GetSite(site1ID)
	if err != nil {
		t.Fatalf("GetSite failed: %v", err)
	}
	if s1.Name != "HQ Paris" || s1.CountryCode != "FR" || s1.NotCorporateSite != false {
		t.Errorf("unexpected site 1 data: %+v", s1)
	}
	if len(s1.FloorplanIDs) != 1 || s1.FloorplanIDs[0] != fp1 {
		t.Errorf("expected floorplan %d attached to site 1, got %v", fp1, s1.FloorplanIDs)
	}
	if len(s1.Floorplans) != 1 || s1.Floorplans[0].Name != "Floor 1" {
		t.Errorf("expected floorplans loaded in site 1, got %+v", s1.Floorplans)
	}

	s2, err := d.GetSite(site2ID)
	if err != nil {
		t.Fatalf("GetSite 2 failed: %v", err)
	}
	if s2.Name != "Coworking Casablanca" || s2.CountryCode != "MA" || s2.NotCorporateSite != true {
		t.Errorf("unexpected site 2 data: %+v", s2)
	}

	// 5. ListSites
	allSites, err := d.ListSites()
	if err != nil {
		t.Fatalf("ListSites failed: %v", err)
	}
	if len(allSites) != 2 {
		t.Fatalf("expected 2 sites, got %d", len(allSites))
	}

	// 6. GetFloorplansBySite
	fpsSite1, err := d.GetFloorplansBySite(site1ID)
	if err != nil {
		t.Fatalf("GetFloorplansBySite failed: %v", err)
	}
	if len(fpsSite1) != 1 || fpsSite1[0].ID != fp1 || fpsSite1[0].SiteName != "HQ Paris" {
		t.Errorf("unexpected floorplans for site 1: %+v", fpsSite1)
	}

	// 7. ListFloorplans verification (checks site_id and site_name populated)
	allFps, err := d.ListFloorplans()
	if err != nil {
		t.Fatalf("ListFloorplans failed: %v", err)
	}
	if len(allFps) != 2 {
		t.Fatalf("expected 2 floorplans, got %d", len(allFps))
	}
	var foundFp1, foundFp2 *models.Floorplan
	for i := range allFps {
		if allFps[i].ID == fp1 {
			foundFp1 = &allFps[i]
		}
		if allFps[i].ID == fp2 {
			foundFp2 = &allFps[i]
		}
	}
	if foundFp1 == nil || foundFp1.SiteID != site1ID || foundFp1.SiteName != "HQ Paris" {
		t.Errorf("unexpected fp1 site association: %+v", foundFp1)
	}
	if foundFp2 == nil || foundFp2.SiteID != site2ID || foundFp2.SiteName != "Coworking Casablanca" {
		t.Errorf("unexpected fp2 site association: %+v", foundFp2)
	}

	// 8. UpdateSite: reassign both floorplans to site 1 and change properties
	err = d.UpdateSite(models.Site{
		ID:               site1ID,
		Name:             "HQ Paris Updated",
		CountryCode:      "BE",
		NotCorporateSite: true,
		FloorplanIDs:     []int64{fp1, fp2},
	})
	if err != nil {
		t.Fatalf("UpdateSite failed: %v", err)
	}

	s1Updated, err := d.GetSite(site1ID)
	if err != nil {
		t.Fatalf("GetSite updated failed: %v", err)
	}
	if s1Updated.Name != "HQ Paris Updated" || s1Updated.CountryCode != "BE" || !s1Updated.NotCorporateSite {
		t.Errorf("unexpected updated site: %+v", s1Updated)
	}
	if len(s1Updated.FloorplanIDs) != 2 {
		t.Errorf("expected 2 floorplans on site 1, got %d", len(s1Updated.FloorplanIDs))
	}

	// 9. UpdateFloorplanSite and UpdateFloorplanWithSite
	err = d.UpdateFloorplanSite(fp2, site2ID)
	if err != nil {
		t.Fatalf("UpdateFloorplanSite failed: %v", err)
	}
	fp2Get, err := d.GetFloorplan(fp2)
	if err != nil {
		t.Fatalf("GetFloorplan failed: %v", err)
	}
	if fp2Get.SiteID != site2ID || fp2Get.SiteName != "Coworking Casablanca" {
		t.Errorf("unexpected fp2 site after UpdateFloorplanSite: %+v", fp2Get)
	}

	err = d.UpdateFloorplanWithSite(fp2, "Floor 2 Renamed", 0, 1)
	if err != nil {
		t.Fatalf("UpdateFloorplanWithSite failed: %v", err)
	}
	fp2Get, _ = d.GetFloorplan(fp2)
	if fp2Get.Name != "Floor 2 Renamed" || fp2Get.SiteID != 0 || fp2Get.SiteName != "" {
		t.Errorf("unexpected fp2 after detaching site: %+v", fp2Get)
	}

	// 10. DeleteSite detaches floorplans
	_ = d.UpdateFloorplanSite(fp1, site1ID)
	err = d.DeleteSite(site1ID)
	if err != nil {
		t.Fatalf("DeleteSite failed: %v", err)
	}
	s1Deleted, err := d.GetSite(site1ID)
	if err == nil && s1Deleted != nil {
		t.Errorf("expected error getting deleted site, got %+v", s1Deleted)
	}
	fp1AfterDelete, _ := d.GetFloorplan(fp1)
	if fp1AfterDelete.SiteID != 0 {
		t.Errorf("expected floorplan site_id to be reset to 0 after site deletion, got %d", fp1AfterDelete.SiteID)
	}
}

func TestGetSiteReservableSeats(t *testing.T) {
	d := newTestDB(t)

	sID1, _ := d.CreateSite(models.Site{Name: "Site 1", CountryCode: "FR", Seats: 50})
	sID2, _ := d.CreateSite(models.Site{Name: "Site 2", CountryCode: "MA", Seats: 30})

	fp1, _ := d.CreateFloorplanWithSite("FP 1", sID1, 1)
	_, _ = d.CreateSeat(fp1, "S1", 10, 10)
	_, _ = d.CreateSeat(fp1, "S2", 20, 20)

	fp2, _ := d.CreateFloorplanWithSite("FP 2", sID1, 2)
	_, _ = d.CreateSeat(fp2, "S3", 30, 30)

	fp3, _ := d.CreateFloorplanWithSite("FP 3", sID2, 1)
	_, _ = d.CreateSeat(fp3, "S4", 40, 40)

	resMap, err := d.GetSiteReservableSeats()
	if err != nil {
		t.Fatalf("GetSiteReservableSeats failed: %v", err)
	}

	if resMap[sID1] != 3 {
		t.Errorf("expected Site 1 to have 3 seats, got %d", resMap[sID1])
	}
	if resMap[sID2] != 1 {
		t.Errorf("expected Site 2 to have 1 seat, got %d", resMap[sID2])
	}
}

func TestGetUsersBySite(t *testing.T) {
	d := newTestDB(t)

	sID, _ := d.CreateSite(models.Site{Name: "Lyon Tech", CountryCode: "FR"})

	u1, _ := d.CreateLocalUser("lyon_u1@test.com", "Alice Lyon", "pass12345")
	u2, _ := d.CreateLocalUser("lyon_u2@test.com", "Bob Lyon", "pass12345")
	u3, _ := d.CreateLocalUser("other_u3@test.com", "Charlie Other", "pass12345")

	_ = d.UpdateUserSite(u1, sID)
	_ = d.UpdateUserSite(u2, sID)

	users, err := d.GetUsersBySite(sID)
	if err != nil {
		t.Fatalf("GetUsersBySite: %v", err)
	}

	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
	if users[0].SiteName != "Lyon Tech" {
		t.Errorf("expected hydrated SiteName 'Lyon Tech', got %q", users[0].SiteName)
	}
	_ = u3
}

