package apiv2

func notificationIncomingFixtureCases() []fixtureCase {
	return []fixtureCase{
		{name: "notification_incoming", operationID: "receiveIncomingNotification", scenario: "Authenticated admission targets only the verified acting profile.", method: "POST", path: "/api/v2/notifications/incoming", headers: profileOwner(), status: 200, schema: "#/components/schemas/NotificationIncomingResult", assertHeaders: []string{"Cache-Control", "Content-Type"}, body: `{"source":"integration","event_id":"one","title":"Reminder"}`},
		{name: "notification_incoming_capabilities", operationID: "getIncomingNotificationCapabilities", scenario: "The acting profile discovers incoming notification support.", method: "GET", path: "/api/v2/notifications/incoming/capabilities", headers: profileOwner(), status: 200, schema: "#/components/schemas/NotificationIncomingCapability", assertHeaders: []string{"Cache-Control", "Content-Type"}},
	}
}
