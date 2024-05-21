--  This file is part of the eliona project.
--  Copyright © 2022 LEICOM iTEC AG. All Rights Reserved.
--  ______ _ _
-- |  ____| (_)
-- | |__  | |_  ___  _ __   __ _
-- |  __| | | |/ _ \| '_ \ / _` |
-- | |____| | | (_) | | | | (_| |
-- |______|_|_|\___/|_| |_|\__,_|
--
--  THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING
--  BUT NOT LIMITED  TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
--  NON INFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM,
--  DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
--  OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

create schema if not exists ews;

-- Should be editable by eliona frontend.
create table if not exists ews.configuration
(
	id                   bigserial primary key,

	client_id            text not null,
	client_secret        text not null,
	tenant_id            text not null,

	ews_url              text not null,
	username             text not null,
	password             text not null,

	service_user_upn     text not null,
	room_list_upn        text not null,
	booking_app_url      text not null,
	refresh_interval     integer not null default 60,
	request_timeout      integer not null default 120,
	asset_filter         json,
	active               boolean default false,
	enable               boolean default false,
	project_ids          text[],
	user_id              text
);

create table if not exists ews.asset
(
	id               bigserial primary key,
	configuration_id bigserial not null references ews.configuration(id) ON DELETE CASCADE,
	project_id       text      not null,
	global_asset_id  text      not null,
	provider_id      text      not null,
	asset_id         integer,
	sync_state       text      not null
);

create table if not exists ews.unified_booking
-- Booking as an event in organizer's calendar.
(
	id                         bigserial primary key,
	exchange_uid               text unique, -- Unique identifier regardless of perspective; one event might be present in multiple mailboxes (i.e. more invited rooms)
	exchange_organizer_mailbox text,
	booking_id                 int unique
);

create table if not exists ews.room_booking
-- Booking of a specific resource within unified booking
(
	id                  bigserial primary key,
	unified_booking_id  bigserial not null references ews.unified_booking(id) ON DELETE CASCADE,
	exchange_id         text unique -- Always from the resource's perspective
);

-- Makes the new objects available for all other init steps
commit;
