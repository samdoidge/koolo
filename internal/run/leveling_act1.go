package run

import (
	"github.com/hectorgimenez/d2go/pkg/data"
	"github.com/hectorgimenez/d2go/pkg/data/area"
	"github.com/hectorgimenez/d2go/pkg/data/difficulty"
	"github.com/hectorgimenez/d2go/pkg/data/item"
	"github.com/hectorgimenez/d2go/pkg/data/npc"
	"github.com/hectorgimenez/d2go/pkg/data/object"
	"github.com/hectorgimenez/d2go/pkg/data/quest"
	"github.com/hectorgimenez/d2go/pkg/data/stat"
	"github.com/hectorgimenez/koolo/internal/action"
	"github.com/hectorgimenez/koolo/internal/game"
	"github.com/hectorgimenez/koolo/internal/ui"
	"github.com/hectorgimenez/koolo/internal/utils"
	"github.com/lxn/win"
)

func (a Leveling) act1() error {
	running := false
	if running || a.ctx.Data.PlayerUnit.Area != area.RogueEncampment {
		return nil
	}

	running = true

	// Clear Den of Evil til level 3 - might need to run it in each difficulty if we need more than one respec
	if lvl, _ := a.ctx.Data.PlayerUnit.FindStat(stat.Level, 0); lvl.Value < 3 {
		a.ctx.Logger.Debug("Current lvl %s under 3 - Leveling in Den of Evil")
		return NewQuests().clearDenQuest()
	}
	// Do Cold Plains til level 6
	if lvl, _ := a.ctx.Data.PlayerUnit.FindStat(stat.Level, 0); lvl.Value < 6 {
		return a.coldPlains()
	}

	// Do Stony Field until 9
	if lvl, _ := a.ctx.Data.PlayerUnit.FindStat(stat.Level, 0); lvl.Value < 9 {
		return a.stonyField()
	}

	// Do Countess Runs until level 14 - skipping in Hell because cold/fire immune - there's no point getting stuck if our merc isn't geared enough yet

	if lvl, _ := a.ctx.Data.PlayerUnit.FindStat(stat.Level, 0); lvl.Value < 14 || a.ctx.Data.CharacterCfg.Game.Difficulty == difficulty.Nightmare {
		if !a.ctx.Data.CanTeleport() {
			a.ctx.CharacterCfg.Game.Countess.ClearFloors = true
		}
		return NewCountess().Run()
	}

	if a.ctx.Data.Quests[quest.Act1SistersToTheSlaughter].Completed() {
		action.ReturnTown()
		// Do Den of Evil if not complete before moving acts
		if !a.ctx.Data.Quests[quest.Act1DenOfEvil].Completed() {
			NewQuests().clearDenQuest()
		}
		if !a.isCainInTown() && !a.ctx.Data.Quests[quest.Act1TheSearchForCain].Completed() {
			NewQuests().rescueCainQuest()
		}

		action.InteractNPC(npc.Warriv)
		a.ctx.HID.KeySequence(win.VK_HOME, win.VK_DOWN, win.VK_RETURN)

		return nil
	} else {
		return NewAndariel().Run()
	}
}

func (a Leveling) coldPlains() error {
	err := action.MoveToArea(area.ColdPlains)
	if err != nil {
		return err
	}

	return action.ClearCurrentLevel(false, data.MonsterAnyFilter())
}

func (a Leveling) denOfEvil() error {
	err := action.MoveToArea(area.BloodMoor)
	if err != nil {
		return err
	}

	err = action.MoveToArea(area.DenOfEvil)
	if err != nil {
		return err
	}

	action.ClearCurrentLevel(false, data.MonsterAnyFilter())
	action.ReturnTown()
	action.InteractNPC(npc.Akara)
	a.ctx.HID.PressKey(win.VK_ESCAPE)

	return nil
}

func (a Leveling) stonyField() error {
	err := action.WayPoint(area.StonyField)
	if err != nil {
		return err
	}

	return action.ClearCurrentLevel(false, data.MonsterAnyFilter())
}

func (a Leveling) isCainInTown() bool {
	_, found := a.ctx.Data.Monsters.FindOne(npc.DeckardCain5, data.MonsterTypeNone)

	return found
}

func (a Leveling) deckardCain() error {
	action.WayPoint(area.RogueEncampment)
	err := action.WayPoint(area.DarkWood)
	if err != nil {
		return err
	}

	err = action.MoveTo(func() (data.Position, bool) {
		for _, o := range a.ctx.Data.Objects {
			if o.Name == object.InifussTree {
				return o.Position, true
			}
		}
		return data.Position{}, false
	})
	if err != nil {
		return err
	}

	action.ClearAreaAroundPlayer(30, data.MonsterAnyFilter())

	obj, found := a.ctx.Data.Objects.FindOne(object.InifussTree)
	if !found {
		a.ctx.Logger.Debug("InifussTree not found")
	}

	err = action.InteractObject(obj, func() bool {
		updatedObj, found := a.ctx.Data.Objects.FindOne(object.InifussTree)
		if found {
			if !updatedObj.Selectable {
				a.ctx.Logger.Debug("Interacted with InifussTree")
			}
			return !updatedObj.Selectable
		}
		return false
	})
	if err != nil {
		return err
	}

	action.ItemPickup(0)
	action.ReturnTown()
	action.InteractNPC(npc.Akara)
	a.ctx.HID.PressKey(win.VK_ESCAPE)

	//Reuse Tristram Run actions
	err = Tristram{}.Run()
	if err != nil {
		return err
	}

	return nil
}

func (a Leveling) tristram() error {
	return Tristram{}.Run()
}

func (a Leveling) countess() error {
	return Countess{}.Run()
}

func (a Leveling) andariel() error {
	err := action.WayPoint(area.CatacombsLevel2)
	if err != nil {
		return err
	}

	err = action.MoveToArea(area.CatacombsLevel3)
	action.MoveToArea(area.CatacombsLevel4)
	if err != nil {
		return err
	}

	// Return to the city, ensure we have pots and everything, and get some antidote potions
	action.ReturnTown()

	potsToBuy := 4
	if a.ctx.Data.MercHPPercent() > 0 {
		potsToBuy = 8
	}

	action.VendorRefill(false, true)
	action.BuyAtVendor(npc.Akara, action.VendorItemRequest{
		Item:     "AntidotePotion",
		Quantity: potsToBuy,
		Tab:      4,
	})

	a.ctx.HID.PressKeyBinding(a.ctx.Data.KeyBindings.Inventory)

	x := 0
	for _, itm := range a.ctx.Data.Inventory.ByLocation(item.LocationInventory) {
		if itm.Name != "AntidotePotion" {
			continue
		}
		pos := ui.GetScreenCoordsForItem(itm)
		utils.Sleep(500)

		if x > 3 {
			a.ctx.HID.Click(game.LeftButton, pos.X, pos.Y)
			utils.Sleep(300)
			if a.ctx.Data.LegacyGraphics {
				a.ctx.HID.Click(game.LeftButton, ui.MercAvatarPositionXClassic, ui.MercAvatarPositionYClassic)
			} else {
				a.ctx.HID.Click(game.LeftButton, ui.MercAvatarPositionX, ui.MercAvatarPositionY)
			}
		} else {
			a.ctx.HID.Click(game.RightButton, pos.X, pos.Y)
		}
		x++
	}

	a.ctx.HID.PressKey(win.VK_ESCAPE)

	action.UsePortalInTown()
	action.Buff()

	action.MoveTo(func() (data.Position, bool) {
		return andarielAttackPos1, true
	})
	a.ctx.Char.KillAndariel()
	action.ReturnTown()
	action.InteractNPC(npc.Warriv)
	a.ctx.HID.KeySequence(win.VK_HOME, win.VK_DOWN, win.VK_RETURN)

	return nil
}
