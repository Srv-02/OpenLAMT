package ui

import (
	"fyne.io/fyne/v2/lang"

	"github.com/Srv-02/openlamt/internal/ui/widgets"
)

// Help popovers (#8, V14) of the AMT settings on the Settings and Users pages. One entry per
// setting; the id names the row it belongs to. Power, Wi-Fi and System Defense build their own
// popovers. What and When are always set; Wrong and Recover only where the setting carries a risk.

type helpID string

const (
	helpHostName   helpID = "hostName"
	helpDomain     helpID = "domain"
	helpPing       helpID = "ping"
	helpDdns       helpID = "ddns"
	helpEnvDomains helpID = "envDomains"
	helpNetAddress helpID = "netAddress"
	helpNetIP      helpID = "netIP"
	helpKvm        helpID = "kvm"
	helpIder       helpID = "ider"
	helpSol        helpID = "sol"
	helpConsent    helpID = "consent"
	helpClock      helpID = "clock"
	helpTls        helpID = "tls"
	helpTurnOff    helpID = "turnOff"
	helpUserAccess helpID = "userAccess"
	helpUserPerms  helpID = "userPerms"
)

// helpContent returns the popover text of one setting in the current language.
func helpContent(id helpID) widgets.HelpContent {
	switch id {
	case helpHostName:
		return widgets.HelpContent{
			Title:   lang.L("Host name"),
			What:    lang.L("The host name Intel AMT uses for itself. Together with the domain it forms the network name of the device."),
			When:    lang.L("Change it to match the name in your DNS, or when two devices share a name."),
			Wrong:   lang.L("Favorites that use the old name stop working until you edit them."),
			Recover: lang.L("Change the name back, or edit the favorites."),
		}
	case helpDomain:
		return widgets.HelpContent{
			Title:   lang.L("Domain"),
			What:    lang.L("The DNS domain of Intel AMT. Together with the host name it forms the network name of the device."),
			When:    lang.L("Change it when your network uses another domain than the one AMT shows."),
			Wrong:   lang.L("With Dynamic DNS the device may no longer be found under its current name, and favorites that use the name stop working."),
			Recover: lang.L("Set the old domain again, or edit the favorites."),
		}
	case helpPing:
		return widgets.HelpContent{
			Title: lang.L("Respond to ping"),
			What:  lang.L("Intel AMT answers ping (ICMP echo) requests on its own network address."),
			When:  lang.L("Leave it on if monitoring tools check the device with ping. Turn it off if AMT should not answer ping."),
		}
	case helpDdns:
		return widgets.HelpContent{
			Title:   lang.L("Dynamic DNS"),
			What:    lang.L("Intel AMT registers its host name in your DNS server by itself. This needs a DNS server that accepts such updates."),
			When:    lang.L("Turn it on if the device gets changing addresses from DHCP and you want to reach it by name."),
			Wrong:   lang.L("The device may then be reachable under a different name than in your favorites. Turning it off ends the registration."),
			Recover: lang.L("Switch it back here, or edit the favorites."),
		}
	case helpEnvDomains:
		return widgets.HelpContent{
			Title:   lang.L("Intranet domains"),
			What:    lang.L("Intel AMT compares the DNS suffix it gets from DHCP with these domains. If none matches, AMT assumes it is outside your network."),
			When:    lang.L("Only for management from outside your network. On a home or lab network, leave it as it is."),
			Wrong:   lang.L("A wrong entry can make AMT treat your own network as outside, so it may stop answering there."),
			Recover: lang.L("Correct the list here. If the device does not answer any more, use the BIOS (MEBx) at the device."),
		}
	case helpNetAddress:
		return widgets.HelpContent{
			Title:   lang.L("Address"),
			What:    lang.L("Decides whether Intel AMT gets its IP address from a DHCP server or uses a fixed address."),
			When:    lang.L("Use Static if your network has no DHCP server, or if the device must keep the same address."),
			Wrong:   lang.L("A wrong address, mask or gateway makes the device unreachable, also for OpenLAMT."),
			Recover: lang.L("Correct the network settings in the BIOS (MEBx) at the device."),
		}
	case helpNetIP:
		return widgets.HelpContent{
			Title: lang.L("IP address"),
			What:  lang.L("The IP address, subnet mask and gateway that Intel AMT uses now."),
			When:  lang.L("Use it to check where AMT answers. To change it, choose DHCP or Static above."),
		}
	case helpKvm:
		return widgets.HelpContent{
			Title:   lang.L("KVM"),
			What:    lang.L("Allows remote control of the screen, keyboard and mouse of the device through Intel AMT (KVM)."),
			When:    lang.L("Turn it on to use the KVM in OpenLAMT. Turn it off if nobody should take control of the screen."),
			Wrong:   lang.L("Turning it off ends open KVM sessions right away."),
			Recover: lang.L("Turn it on again here."),
		}
	case helpIder:
		return widgets.HelpContent{
			Title:   lang.L("Media redirection (IDE-R)"),
			What:    lang.L("Allows OpenLAMT to offer an ISO or disk image as a virtual drive on the device (IDE-R)."),
			When:    lang.L("Turn it on to boot or install from an image without a USB stick."),
			Wrong:   lang.L("Turning it off ends open media sessions right away."),
			Recover: lang.L("Turn it on again here."),
		}
	case helpSol:
		return widgets.HelpContent{
			Title:   lang.L("Serial over LAN (SOL)"),
			What:    lang.L("Sends the text console of the device over the network (Serial over LAN), for example BIOS text screens."),
			When:    lang.L("Turn it on to use the console in OpenLAMT."),
			Wrong:   lang.L("Turning it off ends open console sessions right away."),
			Recover: lang.L("Turn it on again here."),
		}
	case helpConsent:
		return widgets.HelpContent{
			Title:   lang.L("User consent"),
			What:    lang.L("Decides whether the person at the device must approve a remote session with a code shown on its screen. None never asks, KVM only asks for KVM, All asks for every redirection session."),
			When:    lang.L("Keep it on for computers that people use. Choose None for unattended machines."),
			Wrong:   lang.L("With None, anyone with a valid account can open a session without the person at the device noticing. Some devices do not allow a change."),
			Recover: lang.L("Choose KVM only or All again here."),
		}
	case helpClock:
		return widgets.HelpContent{
			Title: lang.L("AMT clock"),
			What:  lang.L("Intel AMT keeps its own clock. The row shows how far it is off against this computer."),
			When:  lang.L("Sync it when it is off by minutes: TLS certificates and log time stamps depend on the time."),
		}
	case helpTls:
		return widgets.HelpContent{
			Title:   lang.L("TLS certificate"),
			What:    lang.L("The certificate Intel AMT uses for TLS. Its fingerprint identifies the device, and OpenLAMT pins it for this device."),
			When:    lang.L("Change it when you installed a certificate of your own, or when the current one expires."),
			Wrong:   lang.L("AMT restarts TLS with the new certificate. Connections that pinned the old fingerprint must trust the new one."),
			Recover: lang.L("Choose the earlier certificate again, if the device still answers."),
		}
	case helpTurnOff:
		return widgets.HelpContent{
			Title:   lang.L("Turn off AMT"),
			What:    lang.L("Turns off Intel AMT on the network. The device stops answering remotely."),
			When:    lang.L("Only if you do not want to manage the device remotely any more, for example before you pass it on."),
			Wrong:   lang.L("The device stops answering at once: no power control, KVM or media, from any tool."),
			Recover: lang.L("Only the BIOS (MEBx) at the device can turn it back on."),
		}
	case helpUserAccess:
		return widgets.HelpContent{
			Title:   lang.L("Access"),
			What:    lang.L("Decides where the account may sign in: over the network, at the device itself (local), or both."),
			When:    lang.L("Use Network for accounts you use from OpenLAMT. Local is for tools that run on the device itself."),
			Wrong:   lang.L("An account without Network access cannot sign in from OpenLAMT."),
			Recover: lang.L("Sign in with another account and change the access."),
		}
	case helpUserPerms:
		return widgets.HelpContent{
			Title:   lang.L("Permissions"),
			What:    lang.L("The parts of Intel AMT the account may use. Each permission covers one or more AMT realms."),
			When:    lang.L("Give an account only what it needs, for example Power control and KVM, media, console for daily work."),
			Wrong:   lang.L("Without Settings the account cannot use the Settings page. Removing a permission from your own account can lock you out of it."),
			Recover: lang.L("Sign in with the admin account and restore the permissions."),
		}
	}
	return widgets.HelpContent{}
}

// helpButton returns the info button of one setting.
func helpButton(id helpID) *widgets.InfoButton { return widgets.NewInfoButton(helpContent(id)) }

// allHelpIDs lists every setting with a popover from this file.
var allHelpIDs = []helpID{helpHostName, helpDomain, helpPing, helpDdns, helpEnvDomains, helpNetAddress, helpNetIP, helpKvm, helpIder, helpSol, helpConsent, helpClock, helpTls, helpTurnOff, helpUserAccess, helpUserPerms}
