package syncmodel

import "time"

type BookingGroup struct {
	ElionaID       int32
	ExchangeUID    string
	OrganizerEmail string
	Occurrences    []BookingOccurrence
}

type BookingOccurrence struct {
	ElionaID      int32
	InstanceIndex int
	Start         time.Time
	End           time.Time
	Cancelled     bool
	RoomBookings  []RoomBooking
}

type RoomBooking struct {
	AssetID                     int32
	ExchangeIDInResourceMailbox string
	BookingOccurrence           *BookingOccurrence
}

func (ub BookingOccurrence) GetAssetIDs() []int32 {
	assetIDs := make([]int32, len(ub.RoomBookings))
	for i, rb := range ub.RoomBookings {
		assetIDs[i] = rb.AssetID
	}
	return assetIDs
}
